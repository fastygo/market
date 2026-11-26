package migrations

import (
	m "github.com/pocketbase/pocketbase/migrations"

	"github.com/pocketbase/pocketbase/core"
)

func init() {
	m.Register(upUsersAPILock, nil)
}

// upUsersAPILock is expand-only: close public users API registration and
// PocketBase password/OTP auth. Guest checkout still creates accounts in
// Go (Users.Ensure), and the cabinet still checks passwords itself.
func upUsersAPILock(app core.App) error {
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return err
	}
	users.CreateRule = nil
	users.PasswordAuth.Enabled = false
	users.OTP.Enabled = false
	users.OAuth2.Enabled = false
	return app.Save(users)
}
