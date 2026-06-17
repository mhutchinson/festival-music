package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

// Song represents a song in the song pool.
type Song struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Artist      string   `json:"artist"` // YouTube/Spotify link or Artist name
	Notes       string   `json:"notes"`
	SuggestedBy string   `json:"suggested_by"`
	CreatedAt   string   `json:"created_at"`
	Roles       []string `json:"roles"`
	Signups     []Signup `json:"signups"`
}

// Signup represents a musician signing up for a role on a song.
type Signup struct {
	SongID     string `json:"song_id"`
	Role       string `json:"role"`
	Musician   string `json:"musician"`
	SignedUpAt string `json:"signed_up_at"`
}

// SheetsDB handles all read/write operations with the Google Sheet database.
type SheetsDB struct {
	srv           *sheets.Service
	spreadsheetID string
}

// NewSheetsDB initializes the Sheets service client using Application Default Credentials (ADC).
func NewSheetsDB() (*SheetsDB, error) {
	ctx := context.Background()
	spreadsheetID := os.Getenv("SPREADSHEET_ID")
	if spreadsheetID == "" {
		return nil, fmt.Errorf("SPREADSHEET_ID environment variable is not set")
	}

	// option.WithScopes is used to query API. 
	// The client will pick up GOOGLE_APPLICATION_CREDENTIALS automatically.
	srv, err := sheets.NewService(ctx, option.WithScopes(sheets.SpreadsheetsScope))
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve Sheets client: %w", err)
	}

	db := &SheetsDB{
		srv:           srv,
		spreadsheetID: spreadsheetID,
	}

	// Auto-initialize sheets and headers if they don't exist
	if err := db.InitializeSheet(); err != nil {
		log.Printf("Warning during Google Sheet initialization: %v (app will still run)", err)
	}

	return db, nil
}

// InitializeSheet ensures that the 'Songs' and 'Signups' sheets (tabs) exist and have headers.
func (db *SheetsDB) InitializeSheet() error {
	spreadsheet, err := db.srv.Spreadsheets.Get(db.spreadsheetID).Do()
	if err != nil {
		return fmt.Errorf("unable to fetch spreadsheet: %w", err)
	}

	hasSongs := false
	hasSignups := false
	for _, s := range spreadsheet.Sheets {
		if s.Properties.Title == "Songs" {
			hasSongs = true
		}
		if s.Properties.Title == "Signups" {
			hasSignups = true
		}
	}

	var requests []*sheets.Request

	if !hasSongs {
		requests = append(requests, &sheets.Request{
			AddSheet: &sheets.AddSheetRequest{
				Properties: &sheets.SheetProperties{Title: "Songs"},
			},
		})
	}
	if !hasSignups {
		requests = append(requests, &sheets.Request{
			AddSheet: &sheets.AddSheetRequest{
				Properties: &sheets.SheetProperties{Title: "Signups"},
			},
		})
	}

	if len(requests) > 0 {
		batchReq := &sheets.BatchUpdateSpreadsheetRequest{Requests: requests}
		_, err = db.srv.Spreadsheets.BatchUpdate(db.spreadsheetID, batchReq).Do()
		if err != nil {
			return fmt.Errorf("unable to create required sheets: %w", err)
		}
		log.Println("Created missing sheets (Songs/Signups)")
	}

	// Write headers if they are empty
	if err := db.writeHeaderIfEmpty("Songs", []interface{}{"ID", "Title", "Artist", "Notes", "SuggestedBy", "CreatedAt", "Roles"}); err != nil {
		return fmt.Errorf("unable to initialize Songs header: %w", err)
	}
	if err := db.writeHeaderIfEmpty("Signups", []interface{}{"SongID", "Role", "Musician", "SignedUpAt"}); err != nil {
		return fmt.Errorf("unable to initialize Signups header: %w", err)
	}

	return nil
}

func (db *SheetsDB) writeHeaderIfEmpty(sheetName string, headers []interface{}) error {
	resp, err := db.srv.Spreadsheets.Values.Get(db.spreadsheetID, fmt.Sprintf("%s!A1:1", sheetName)).Do()
	if err != nil {
		return err
	}
	if len(resp.Values) == 0 || len(resp.Values[0]) == 0 {
		valueRange := &sheets.ValueRange{
			Values: [][]interface{}{headers},
		}
		_, err = db.srv.Spreadsheets.Values.Update(
			db.spreadsheetID,
			fmt.Sprintf("%s!A1", sheetName),
			valueRange,
		).ValueInputOption("RAW").Do()
		if err != nil {
			return err
		}
		log.Printf("Initialized headers for sheet: %s", sheetName)
	}
	return nil
}

// GetSongs fetches all songs and their corresponding signups.
func (db *SheetsDB) GetSongs() ([]Song, error) {
	// 1. Fetch Songs data (excluding header)
	songsResp, err := db.srv.Spreadsheets.Values.Get(db.spreadsheetID, "Songs!A2:G").Do()
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve songs data: %w", err)
	}

	var songs []Song
	songMap := make(map[string]*Song)

	for _, row := range songsResp.Values {
		id := getString(row, 0)
		if id == "" {
			continue
		}
		var roles []string
		rolesStr := getString(row, 6)
		if rolesStr != "" {
			for _, r := range strings.Split(rolesStr, ",") {
				r = strings.TrimSpace(r)
				if r != "" {
					roles = append(roles, r)
				}
			}
		}
		song := Song{
			ID:          id,
			Title:       getString(row, 1),
			Artist:      getString(row, 2),
			Notes:       getString(row, 3),
			SuggestedBy: getString(row, 4),
			CreatedAt:   getString(row, 5),
			Roles:       roles,
			Signups:     []Signup{},
		}
		songs = append(songs, song)
		// Store pointer to update signups later
		songMap[id] = &songs[len(songs)-1]
	}

	// 2. Fetch Signups data (excluding header)
	signupsResp, err := db.srv.Spreadsheets.Values.Get(db.spreadsheetID, "Signups!A2:D").Do()
	if err != nil {
		// If signups fails or sheet is empty, return songs as is
		log.Printf("Warning: signups retrieval returned error: %v", err)
		return songs, nil
	}

	for _, row := range signupsResp.Values {
		songID := getString(row, 0)
		role := getString(row, 1)
		musician := getString(row, 2)
		signedUpAt := getString(row, 3)

		if songID == "" || role == "" || musician == "" {
			continue
		}

		signup := Signup{
			SongID:     songID,
			Role:       role,
			Musician:   musician,
			SignedUpAt: signedUpAt,
		}

		if songPtr, ok := songMap[songID]; ok {
			songPtr.Signups = append(songPtr.Signups, signup)
		}
	}

	return songs, nil
}

// AddSong adds a new song to the sheet, along with any initial signups.
func (db *SheetsDB) AddSong(song Song) error {
	createdAt := time.Now().Format(time.RFC3339)
	song.CreatedAt = createdAt

	// Serialize roles list to a comma-separated string
	rolesStr := strings.Join(song.Roles, ",")

	// 1. Append song metadata
	songValueRange := &sheets.ValueRange{
		Values: [][]interface{}{{
			song.ID,
			song.Title,
			song.Artist,
			song.Notes,
			song.SuggestedBy,
			song.CreatedAt,
			rolesStr,
		}},
	}
	_, err := db.srv.Spreadsheets.Values.Append(
		db.spreadsheetID,
		"Songs!A:G",
		songValueRange,
	).ValueInputOption("RAW").Do()
	if err != nil {
		return fmt.Errorf("unable to append song: %w", err)
	}

	// 2. Append any initial signups
	if len(song.Signups) > 0 {
		var signupValues [][]interface{}
		signedUpAt := time.Now().Format(time.RFC3339)
		for _, s := range song.Signups {
			if s.Musician == "" {
				continue
			}
			signupValues = append(signupValues, []interface{}{
				song.ID,
				s.Role,
				s.Musician,
				signedUpAt,
			})
		}

		if len(signupValues) > 0 {
			signupValueRange := &sheets.ValueRange{Values: signupValues}
			_, err = db.srv.Spreadsheets.Values.Append(
				db.spreadsheetID,
				"Signups!A:D",
				signupValueRange,
			).ValueInputOption("RAW").Do()
			if err != nil {
				return fmt.Errorf("unable to append initial signups: %w", err)
			}
		}
	}

	return nil
}

// AddSignup registers a musician for a role on a specific song.
func (db *SheetsDB) AddSignup(songID string, role string, musician string) error {
	signedUpAt := time.Now().Format(time.RFC3339)
	valueRange := &sheets.ValueRange{
		Values: [][]interface{}{{
			songID,
			role,
			musician,
			signedUpAt,
		}},
	}
	_, err := db.srv.Spreadsheets.Values.Append(
		db.spreadsheetID,
		"Signups!A:D",
		valueRange,
	).ValueInputOption("RAW").Do()
	if err != nil {
		return fmt.Errorf("unable to sign up for role: %w", err)
	}

	return nil
}

// Helper function to safely read string cell values
func getString(row []interface{}, idx int) string {
	if idx < len(row) {
		if val, ok := row[idx].(string); ok {
			return val
		}
		// If it's a number/float in Sheets, convert it to a string representation
		return fmt.Sprintf("%v", row[idx])
	}
	return ""
}

// GetSignupForRole returns the signup for the specified role if it exists, otherwise nil.
func (s Song) GetSignupForRole(role string) *Signup {
	for _, signup := range s.Signups {
		if signup.Role == role {
			return &signup
		}
	}
	return nil
}

