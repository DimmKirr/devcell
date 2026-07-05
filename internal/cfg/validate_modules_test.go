package cfg_test

import (
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/cfg"
)

// TestValidateModulesAgainstCatalog_AllValid: every name in the user list is
// in the catalog → nil error.
func TestValidateModulesAgainstCatalog_AllValid(t *testing.T) {
	catalog := []string{"electronics", "yahoo-finance", "kicad", "plex"}
	user := []string{"electronics", "plex"}
	if err := cfg.ValidateModulesAgainstCatalog(user, catalog); err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
}

// TestValidateModulesAgainstCatalog_EmptyUserList: no modules to validate → ok.
func TestValidateModulesAgainstCatalog_EmptyUserList(t *testing.T) {
	catalog := []string{"electronics", "plex"}
	if err := cfg.ValidateModulesAgainstCatalog(nil, catalog); err != nil {
		t.Errorf("empty user list should be valid, got: %v", err)
	}
}

// TestValidateModulesAgainstCatalog_UnknownNameNamesItInError: error must
// include the offending name so users can grep their config.
func TestValidateModulesAgainstCatalog_UnknownNameNamesItInError(t *testing.T) {
	catalog := []string{"electronics", "yahoo-finance"}
	user := []string{"electronics", "frob"}
	err := cfg.ValidateModulesAgainstCatalog(user, catalog)
	if err == nil {
		t.Fatal("expected error for unknown module 'frob', got nil")
	}
	if !strings.Contains(err.Error(), "frob") {
		t.Errorf("error must mention unknown name 'frob', got: %v", err)
	}
}

// TestValidateModulesAgainstCatalog_ErrorListsAvailableNames: when a name is
// invalid, the error should hint at the catalog so the user can self-correct.
func TestValidateModulesAgainstCatalog_ErrorListsAvailableNames(t *testing.T) {
	catalog := []string{"electronics", "yahoo-finance", "kicad"}
	user := []string{"yahoo-finanace"} // typo
	err := cfg.ValidateModulesAgainstCatalog(user, catalog)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"yahoo-finanace", "yahoo-finance", "kicad", "electronics"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error should mention %q, got: %s", want, msg)
		}
	}
}

// TestValidateModulesAgainstCatalog_MultipleUnknownAllReported: when several
// names are bad, the error should list all of them in one go.
func TestValidateModulesAgainstCatalog_MultipleUnknownAllReported(t *testing.T) {
	catalog := []string{"electronics"}
	user := []string{"frob", "bar", "electronics", "baz"}
	err := cfg.ValidateModulesAgainstCatalog(user, catalog)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{"frob", "bar", "baz"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error should report bad name %q in one shot, got: %s", want, msg)
		}
	}
}
