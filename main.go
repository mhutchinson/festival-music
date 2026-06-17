package main

import (
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
)

var (
	indexTmpl   *template.Template
	suggestTmpl *template.Template
)

func main() {
	// Parse command-line flags
	portFlag := flag.String("port", "", "Port to run the server on (falls back to PORT env var or 8080)")
	flag.Parse()

	// 1. Verify environment setup
	spreadsheetID := os.Getenv("SPREADSHEET_ID")
	if spreadsheetID == "" {
		log.Println("Warning: SPREADSHEET_ID environment variable not set. App will fail to initialize database client.")
	}

	// 2. Initialize database connection
	db, err := NewSheetsDB()
	if err != nil {
		log.Printf("Database initialization failed: %v", err)
		log.Println("Ensure SPREADSHEET_ID and GOOGLE_APPLICATION_CREDENTIALS are configured.")
	}

	// 3. Load HTML templates
	loadTemplates()

	// 4. Setup Serve Mux (Go 1.22+ supports HTTP method & wildcard path parameters)
	mux := http.NewServeMux()

	// Static files routing
	fs := http.FileServer(http.Dir("./static"))
	mux.Handle("GET /static/", http.StripPrefix("/static/", fs))

	// Application routes
	mux.HandleFunc("GET /", handleIndex(db))
	mux.HandleFunc("GET /suggest", handleSuggest(db))
	mux.HandleFunc("POST /suggest", handleSuggest(db))
	mux.HandleFunc("POST /songs/{id}/signup", handleSignup(db))

	// 5. Start server
	port := *portFlag
	if port == "" {
		port = os.Getenv("PORT")
	}
	if port == "" {
		port = "8080"
	}
	log.Printf("Server starting on http://localhost:%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}

// loadTemplates parses all templates and registers the helper functions in isolated namespaces.
func loadTemplates() {
	funcMap := template.FuncMap{
		"hasPrefix": strings.HasPrefix,
	}

	var err error
	indexTmpl, err = template.New("").Funcs(funcMap).ParseFiles(
		"templates/base.html",
		"templates/index.html",
		"templates/components.html",
	)
	if err != nil {
		log.Fatalf("Fatal error parsing index templates: %v", err)
	}

	suggestTmpl, err = template.New("").Funcs(funcMap).ParseFiles(
		"templates/base.html",
		"templates/suggest.html",
		"templates/components.html",
	)
	if err != nil {
		log.Fatalf("Fatal error parsing suggest templates: %v", err)
	}
}

// handleIndex serves the list of songs, allowing search and role filters.
func handleIndex(db *SheetsDB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if db == nil {
			http.Error(w, "Database is uninitialized. Configure SPREADSHEET_ID and GOOGLE_APPLICATION_CREDENTIALS.", http.StatusInternalServerError)
			return
		}

		q := r.URL.Query().Get("q")
		openOnly := r.URL.Query().Get("open_only") == "true"

		songs, err := db.GetSongs()
		if err != nil {
			log.Printf("Error fetching songs from sheet: %v", err)
			http.Error(w, "Error retrieving song list from database", http.StatusInternalServerError)
			return
		}

		filteredSongs := filterSongs(songs, q, openOnly)

		data := struct {
			Songs    []Song
			Query    string
			OpenOnly bool
		}{
			Songs:    filteredSongs,
			Query:    q,
			OpenOnly: openOnly,
		}

		// If HTMX request, render only the song list partial
		if r.Header.Get("HX-Request") == "true" {
			err = indexTmpl.ExecuteTemplate(w, "song-list", data)
		} else {
			err = indexTmpl.ExecuteTemplate(w, "index.html", data)
		}

		if err != nil {
			log.Printf("Template execution failed: %v", err)
		}
	}
}

// handleSuggest handles displaying the song proposal form and submitting it.
func handleSuggest(db *SheetsDB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if db == nil {
			http.Error(w, "Database is uninitialized. Configure SPREADSHEET_ID and GOOGLE_APPLICATION_CREDENTIALS.", http.StatusInternalServerError)
			return
		}

		if r.Method == http.MethodGet {
			data := struct {
				StandardRoles []string
			}{
				StandardRoles: []string{
					"Lead Vocals",
					"Backing Vocals",
					"Lead Guitar",
					"Rhythm Guitar",
					"Bass",
					"Drums",
					"Keyboards",
				},
			}
			err := suggestTmpl.ExecuteTemplate(w, "suggest.html", data)
			if err != nil {
				log.Printf("Template execution failed: %v", err)
			}
			return
		}

		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "Invalid form data", http.StatusBadRequest)
				return
			}

			title := strings.TrimSpace(r.FormValue("title"))
			artist := strings.TrimSpace(r.FormValue("artist"))
			suggestedBy := strings.TrimSpace(r.FormValue("suggested_by"))
			notes := strings.TrimSpace(r.FormValue("notes"))

			if title == "" || suggestedBy == "" {
				http.Error(w, "Title and Suggested By fields are required", http.StatusBadRequest)
				return
			}

			songID := uuid.New().String()

			// Extract standard roles and signups
			var roles []string
			var signups []Signup

			selectedRoles := r.Form["roles"]
			for _, role := range selectedRoles {
				roles = append(roles, role)
				assignee := strings.TrimSpace(r.FormValue("assignee_" + role))
				if assignee != "" {
					signups = append(signups, Signup{
						SongID:   songID,
						Role:     role,
						Musician: assignee,
					})
				}
			}

			// Extract custom roles (up to 3)
			for i := 1; i <= 3; i++ {
				customRole := strings.TrimSpace(r.FormValue(fmt.Sprintf("custom_role_%d", i)))
				if customRole != "" {
					roles = append(roles, customRole)
					assignee := strings.TrimSpace(r.FormValue(fmt.Sprintf("custom_assignee_%d", i)))
					if assignee != "" {
						signups = append(signups, Signup{
							SongID:   songID,
							Role:     customRole,
							Musician: assignee,
						})
					}
				}
			}

			song := Song{
				ID:          songID,
				Title:       title,
				Artist:      artist,
				Notes:       notes,
				SuggestedBy: suggestedBy,
				Roles:       roles,
				Signups:     signups,
			}

			if err := db.AddSong(song); err != nil {
				log.Printf("Error adding song to Google Sheet: %v", err)
				http.Error(w, "Error writing song to database", http.StatusInternalServerError)
				return
			}

			// Redirect to homepage on success
			http.Redirect(w, r, "/", http.StatusSeeOther)
		}
	}
}

// handleSignup processes a musician signing up for a specific role of a song.
func handleSignup(db *SheetsDB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if db == nil {
			http.Error(w, "Database is uninitialized.", http.StatusInternalServerError)
			return
		}

		songID := r.PathValue("id")
		if songID == "" {
			http.Error(w, "Song ID is required", http.StatusBadRequest)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid signup details", http.StatusBadRequest)
			return
		}

		role := strings.TrimSpace(r.FormValue("role"))
		musician := strings.TrimSpace(r.FormValue("musician"))

		if role == "" || musician == "" {
			http.Error(w, "Role and Musician fields are required", http.StatusBadRequest)
			return
		}

		// Add signup
		if err := db.AddSignup(songID, role, musician); err != nil {
			log.Printf("Error adding signup to sheet: %v", err)
			http.Error(w, "Error updating signup in database", http.StatusInternalServerError)
			return
		}

		// Reload songs list to build updated song component
		songs, err := db.GetSongs()
		if err != nil {
			log.Printf("Error reloading songs list: %v", err)
			http.Error(w, "Error synchronizing database", http.StatusInternalServerError)
			return
		}

		var targetSong *Song
		for i := range songs {
			if songs[i].ID == songID {
				targetSong = &songs[i]
				break
			}
		}

		if targetSong == nil {
			http.Error(w, "Song not found after update", http.StatusNotFound)
			return
		}

		// Return only the updated song card partial
		err = indexTmpl.ExecuteTemplate(w, "song-card", targetSong)
		if err != nil {
			log.Printf("Error rendering song-card component: %v", err)
		}
	}
}

// filterSongs processes client-side query matching and open-roles filter.
func filterSongs(songs []Song, query string, openOnly bool) []Song {
	var filtered []Song
	query = strings.ToLower(strings.TrimSpace(query))

	for _, s := range songs {
		if openOnly {
			hasOpenRole := false
			for _, role := range s.Roles {
				if s.GetSignupForRole(role) == nil {
					hasOpenRole = true
					break
				}
			}
			if !hasOpenRole {
				continue
			}
		}

		if query != "" {
			match := strings.Contains(strings.ToLower(s.Title), query) ||
				strings.Contains(strings.ToLower(s.Artist), query) ||
				strings.Contains(strings.ToLower(s.Notes), query) ||
				strings.Contains(strings.ToLower(s.SuggestedBy), query)
			if !match {
				continue
			}
		}

		filtered = append(filtered, s)
	}

	return filtered
}
