# Festival Music Signup Web Application

A simple, lightweight web application to organize musicians to sign up to play songs at a music festival. 

Built in **Go** using **HTMX** for dynamic, single-page-style updates and **Vanilla CSS** with a retro vinyl record aesthetic. The application uses a standard Google Sheet as its database, allowing organizers to easily inspect, correlate, and edit data.

---

## 🛠️ Tech Stack & Architecture

- **Backend**: Go (using standard library `net/http` and `html/template`).
- **Database**: Google Sheets (via the Google Sheets API v4).
- **Frontend**: HTMX (local) + Vanilla CSS (No build/compile steps, lightweight, serverless-ready).
- **Deployment**: Primary deploy on **GCP Cloud Run** (Serverless Free Tier), with VM-based Docker deployment supported as an alternative.
- **License**: Apache 2.0.

---

## 📊 Database Schema (Google Sheets)

The application requires a single Google Spreadsheet with two tabs (sheets):

### 1. `Songs` Tab
Stores metadata about the suggested songs.

| Column | Header | Type | Description |
| :--- | :--- | :--- | :--- |
| **A** | `ID` | String / UUID | Unique identifier for the song |
| **B** | `Title` | String | Title of the song |
| **C** | `Artist` | String | Artist name / Link (YouTube/Spotify) |
| **D** | `Notes` | String | Key change requests, tempo, specific instructions |
| **E** | `SuggestedBy` | String | Name of the person who suggested the song |
| **F** | `CreatedAt` | Timestamp | Date and time suggested |
| **G** | `Roles` | String | Comma-separated list of requested musician roles |

### 2. `Signups` Tab
Stores musician signups for roles.

| Column | Header | Type | Description |
| :--- | :--- | :--- | :--- |
| **A** | `SongID` | String | Relates to `ID` in the `Songs` tab |
| **B** | `Role` | String | Role name (e.g., Lead Vocals, Bass, Trombone) |
| **C** | `Musician` | String | Name of the musician |
| **D** | `SignedUpAt` | Timestamp | Date and time of signup |

---

## ⚙️ Configuration & Environment Variables

The application is configured using environment variables:

1. **`SPREADSHEET_ID`**: The unique identifier of your Google Sheet (extracted from the sheet's URL: `https://docs.google.com/spreadsheets/d/[SPREADSHEET_ID]/edit`).
2. **`GOOGLE_APPLICATION_CREDENTIALS`**: Path to your Google Service Account credentials JSON file (only needed for local running or VM deployment; **not** needed on Cloud Run).

### Google Sheets API Setup

1. Go to the [Google Cloud Console](https://console.cloud.google.com/).
2. Create a project and enable the **Google Sheets API**.
3. Create a **Service Account** under IAM & Admin > Service Accounts.
4. Generate a **JSON Key** for the Service Account and download it (for local testing).
5. **Important**: Open your Google Sheet in the browser, click **Share**, and share it with the email address of your Service Account with **Editor** permissions.

---

## 🎨 Theme & Vibe

The UI is styled with a **Retro Vinyl** theme:
- Warm vintage colors (cream background `#F4EAD4`, rust accents `#D25A3C`, mustard yellow `#D69E2E`, charcoal borders `#1C1B19`).
- Bold, stylized borders and drop shadows.
- Retro-style fonts.

---

## 🚀 Running Locally

1. Install Go 1.26 or newer.
2. Clone the repository.
3. Set your environment variables:
   ```bash
   export SPREADSHEET_ID="your-spreadsheet-id-here"
   export GOOGLE_APPLICATION_CREDENTIALS="/path/to/your/service-account.json"
   ```
4. Start the server (default port is `8080`):
   ```bash
   go run .
   ```
   Or run on a custom port (ideal for running staging/test instances side-by-side):
   ```bash
   go run . -port=8081
   ```
5. Open your browser and navigate to the selected port (e.g., `http://localhost:8080` or `http://localhost:8081`).

---

## ☁️ Primary Deployment Option: GCP Cloud Run (Serverless)

We recommend deploying to **GCP Cloud Run** using the **GCP Free Tier**, which easily covers hundreds of thousands of signups and page views per month. 

### Why Cloud Run?
*   **$0 cost**: Scales down to 0 instances when idle, incurring zero charges.
*   **Keyless Security**: Cloud Run runs under a GCP Service Account. You do not need to upload or configure any `credentials.json` files. You only share the Google Sheet with your Cloud Run Service Account's email address as an **Editor**.
*   **Auto SSL**: HTTPS certificates are fully managed by Google.

### Deployment Commands:
1. Build and push your image to GCP Artifact Registry.
2. Deploy the service to Cloud Run:
   ```bash
   gcloud run deploy festival-music \
     --image us-central1-docker.pkg.dev/YOUR_PROJECT_ID/YOUR_REPO/music-app:latest \
     --region us-central1 \
     --update-env-vars SPREADSHEET_ID=your-sheet-id \
     --allow-unauthenticated
   ```

---

## 🖥️ Alternative Deployment Option: GCP VM (Docker Compose)

If you prefer to host on a persistent VM instance (like a free GCP `e2-micro`), you can run the app inside Docker using Docker Compose and Watchtower for automated updates.

1. Copy [docker-compose.yml](docker-compose.yml) and your Google Service Account `credentials.json` file to the VM.
2. Set your `SPREADSHEET_ID` in a `.env` file on the VM.
3. Start the application:
   ```bash
   docker compose up -d
   ```
4. **Watchtower** will poll your container registry (e.g. GitHub Container Registry or GCP Artifact Registry) every 5 minutes and automatically update and restart the app container whenever you push a new image.

---

## 📝 License

Distributed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for details.
