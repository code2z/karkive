package v1alpha1

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestBackupWebhookRejectsBadSchedule(t *testing.T) {
	v := &BackupCustomValidator{}
	backup := &Backup{
		Spec: BackupSpec{
			Schedule:  "never",
			Database:  DatabaseSpec{Host: "postgres.example.svc.cluster.local", Name: "app"},
			S3:        S3Spec{Path: "app/pgdump"},
			SecretRef: corev1.LocalObjectReference{Name: "creds"},
		},
	}
	if _, err := v.ValidateCreate(context.Background(), backup); err == nil {
		t.Fatal("expected reject")
	}
	backup.Spec.Schedule = "0 2 * * *"
	if _, err := v.ValidateCreate(context.Background(), backup); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreWebhookRejectsWrongType(t *testing.T) {
	v := &RestoreCustomValidator{}
	if _, err := v.ValidateCreate(context.Background(), &Backup{}); err == nil {
		t.Fatal("expected type error")
	}
}

func TestWebhooksRejectUnknownEncryptionMethod(t *testing.T) {
	backup := &Backup{Spec: BackupSpec{
		Schedule:   "0 2 * * *",
		Database:   DatabaseSpec{Host: "h", Name: "app"},
		S3:         S3Spec{Path: "app/pgdump"},
		SecretRef:  corev1.LocalObjectReference{Name: "creds"},
		Encryption: &EncryptionSpec{Method: "rot13"},
	}}
	bv := &BackupCustomValidator{}
	if _, err := bv.ValidateCreate(context.Background(), backup); err == nil {
		t.Fatal("backup: expected reject")
	}
	if _, err := bv.ValidateUpdate(context.Background(), backup, backup); err == nil {
		t.Fatal("backup update: expected reject")
	}
	backup.Spec.Encryption.Method = EncryptionMethodAge
	if _, err := bv.ValidateCreate(context.Background(), backup); err != nil {
		t.Fatal(err)
	}
	restore := &Restore{Spec: RestoreSpec{
		Schedule:       "0 2 * * *",
		Database:       DatabaseSpec{Host: "h", Name: "app"},
		S3:             S3Spec{Path: "app/pgdump"},
		SecretRef:      corev1.LocalObjectReference{Name: "creds"},
		PostgresSecret: &SecretKeySelector{Name: "pg"},
		Encryption:     &EncryptionSpec{Method: "rot13"},
	}}
	rv := &RestoreCustomValidator{}
	if _, err := rv.ValidateCreate(context.Background(), restore); err == nil {
		t.Fatal("restore: expected reject")
	}
	restore.Spec.Encryption.Method = EncryptionMethodAge
	if _, err := rv.ValidateCreate(context.Background(), restore); err != nil {
		t.Fatal(err)
	}
}
