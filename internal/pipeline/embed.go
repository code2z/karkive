package pipeline

import (
	"embed"
	"fmt"
	"strings"
)

//go:embed scripts/common.sh
var commonScript string

//go:embed scripts/backup/*.sh
var backupScripts embed.FS

//go:embed scripts/restore/*.sh
var restoreScripts embed.FS

//go:embed scripts/stream/*.sh
var streamScripts embed.FS

// CommonScript returns the shared helper sourced by every backup and restore stage.
func CommonScript() string {
	return commonScript
}

func composeStage(body string) string {
	common := commonScript
	if !strings.HasSuffix(common, "\n") {
		common += "\n"
	}
	return common + body
}

// BackupScript returns the named file from scripts/backup/, prepended with common.sh.
func BackupScript(name string) (string, error) {
	b, err := backupScripts.ReadFile("scripts/backup/" + name)
	if err != nil {
		return "", fmt.Errorf("embedded backup script %s: %w", name, err)
	}
	return composeStage(string(b)), nil
}

// MustBackupScript is BackupScript and panics on a missing file (compile-time embed).
func MustBackupScript(name string) string {
	s, err := BackupScript(name)
	if err != nil {
		panic(err)
	}
	return s
}

// RestoreScript returns the named file from scripts/restore/, prepended with common.sh.
func RestoreScript(name string) (string, error) {
	b, err := restoreScripts.ReadFile("scripts/restore/" + name)
	if err != nil {
		return "", fmt.Errorf("embedded restore script %s: %w", name, err)
	}
	return composeStage(string(b)), nil
}

// MustRestoreScript is RestoreScript and panics on a missing file.
func MustRestoreScript(name string) string {
	s, err := RestoreScript(name)
	if err != nil {
		panic(err)
	}
	return s
}

// StreamScript returns scripts/stream/<name> (backup.sh or restore.sh) prepended with scripts/stream/common.sh. It runs under bash in the single Stream container.
func StreamScript(name string) (string, error) {
	common, err := streamScripts.ReadFile("scripts/stream/common.sh")
	if err != nil {
		return "", fmt.Errorf("embedded stream common.sh: %w", err)
	}
	b, err := streamScripts.ReadFile("scripts/stream/" + name)
	if err != nil {
		return "", fmt.Errorf("embedded stream script %s: %w", name, err)
	}
	c := string(common)
	if !strings.HasSuffix(c, "\n") {
		c += "\n"
	}
	return c + string(b), nil
}

// MustStreamScript is StreamScript and panics on a missing file.
func MustStreamScript(name string) string {
	s, err := StreamScript(name)
	if err != nil {
		panic(err)
	}
	return s
}
