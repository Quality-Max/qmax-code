package main

// ⚠️ DEMO FILE — intentional vulnerabilities to trigger QualityMax pipeline findings.
// DO NOT MERGE. Delete after demo.

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"os/exec"
)

// FINDING 1: Hardcoded API secret
var stripeSecretKey = "sk_live_4eC39HqLyjWDarjtT1zdp7dc"

// FINDING 2: SQL injection — string concatenation into query
func unsafeUserLookup(db *sql.DB, userID string) (*sql.Row, error) {
	query := fmt.Sprintf("SELECT * FROM users WHERE id = '%s'", userID)
	return db.QueryRow(query), nil
}

// FINDING 3: Command injection — unsanitized input to exec
func runUserCommand(input string) ([]byte, error) {
	return exec.Command("sh", "-c", input).Output()
}

// FINDING 4: Open redirect — unvalidated redirect target
func handleRedirect(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("url")
	http.Redirect(w, r, target, http.StatusFound)
}

// FINDING 5: Sensitive data in error response
func handleLogin(w http.ResponseWriter, r *http.Request) {
	password := r.FormValue("password")
	if password != os.Getenv("ADMIN_PASSWORD") {
		http.Error(w, fmt.Sprintf("Login failed for password: %s", password), http.StatusUnauthorized)
	}
}
