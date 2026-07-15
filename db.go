package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
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
	cacheMu       sync.RWMutex
	cachedSongs   []Song
	lastFetch     time.Time
	cacheTTL      time.Duration
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
		cacheTTL:      5 * time.Second,
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
	db.cacheMu.RLock()
	if time.Since(db.lastFetch) < db.cacheTTL && db.cachedSongs != nil {
		songs := make([]Song, len(db.cachedSongs))
		copy(songs, db.cachedSongs)
		db.cacheMu.RUnlock()
		return songs, nil
	}
	db.cacheMu.RUnlock()

	db.cacheMu.Lock()
	defer db.cacheMu.Unlock()

	// Double check condition after acquiring write lock
	if time.Since(db.lastFetch) < db.cacheTTL && db.cachedSongs != nil {
		songs := make([]Song, len(db.cachedSongs))
		copy(songs, db.cachedSongs)
		return songs, nil
	}

	songs, err := db.fetchSongsFromSheets()
	if err != nil {
		// Fallback to expired cache under API rate limits or Sheets downtime
		if db.cachedSongs != nil {
			log.Printf("Warning: Fetching from Sheets failed (%v), falling back to expired cache", err)
			songs := make([]Song, len(db.cachedSongs))
			copy(songs, db.cachedSongs)
			return songs, nil
		}
		return nil, err
	}

	db.cachedSongs = songs
	db.lastFetch = time.Now()

	// Return a copy to prevent race conditions
	songsCopy := make([]Song, len(songs))
	copy(songsCopy, songs)
	return songsCopy, nil
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

	db.invalidateCache()
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

	db.invalidateCache()
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

// invalidateCache clears the last fetch time to force reload on next read.
func (db *SheetsDB) invalidateCache() {
	db.cacheMu.Lock()
	db.lastFetch = time.Time{}
	db.cacheMu.Unlock()
}

// fetchSongsFromSheets retrieves data from Google Sheets in a single BatchGet call.
func (db *SheetsDB) fetchSongsFromSheets() ([]Song, error) {
	resp, err := db.srv.Spreadsheets.Values.BatchGet(db.spreadsheetID).
		Ranges("Songs!A2:G", "Signups!A2:D").Do()
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve batch data: %w", err)
	}

	if len(resp.ValueRanges) < 2 {
		return nil, fmt.Errorf("unexpected value ranges returned: %d", len(resp.ValueRanges))
	}

	songsRange := resp.ValueRanges[0]
	signupsRange := resp.ValueRanges[1]

	var songs []Song
	songMap := make(map[string]int)

	for _, row := range songsRange.Values {
		id := strings.TrimSpace(getString(row, 0))
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
		songMap[id] = len(songs) - 1
	}

	for _, row := range signupsRange.Values {
		songID := strings.TrimSpace(getString(row, 0))
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

		if idx, ok := songMap[songID]; ok {
			songs[idx].Signups = append(songs[idx].Signups, signup)
		}
	}

	return songs, nil
}

