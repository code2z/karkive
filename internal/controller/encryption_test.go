package controller

import (
	"context"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	karkivev1alpha1 "github.com/mahdidarabi/KArkive/api/v1alpha1"
	"github.com/mahdidarabi/KArkive/internal/ptr"
	"github.com/mahdidarabi/KArkive/internal/resources"
)

const (
	testAgeRecipient = "age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p"
	testAgeIdentity  = "AGE-SECRET-KEY-1QQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQ"
)

func ageEncryption() *karkivev1alpha1.EncryptionSpec {
	return &karkivev1alpha1.EncryptionSpec{Method: karkivev1alpha1.EncryptionMethodAge}
}

func streamPipeline() *karkivev1alpha1.PipelineSpec {
	return &karkivev1alpha1.PipelineSpec{Mode: karkivev1alpha1.PipelineModeStream}
}

// ageBackupSecret has the public key and no gpg_passphrase.
func ageBackupSecret(base *corev1.Secret) *corev1.Secret {
	s := base.DeepCopy()
	delete(s.Data, "gpg_passphrase")
	s.Data["age_recipient"] = []byte(testAgeRecipient)
	return s
}

func assertBackupReady(t *testing.T, updated *karkivev1alpha1.Backup) {
	t.Helper()
	if updated.Status.Phase != karkivev1alpha1.BackupPhaseReady {
		cond := meta.FindStatusCondition(updated.Status.Conditions, karkivev1alpha1.ConditionReady)
		t.Fatalf("phase=%q condition=%v", updated.Status.Phase, cond)
	}
}

func assertSecretInvalid(t *testing.T, conds []metav1.Condition, phase, wantPhase, missingKey string) {
	t.Helper()
	cond := meta.FindStatusCondition(conds, karkivev1alpha1.ConditionReady)
	if phase != wantPhase || cond == nil || cond.Reason != "SecretInvalid" || !strings.Contains(cond.Message, missingKey) {
		t.Fatalf("phase=%q condition=%v, want SecretInvalid for %q", phase, cond, missingKey)
	}
}

func TestBackupReconcile_AgeNeedsRecipientNotPassphrase(t *testing.T) {
	for _, mode := range []*karkivev1alpha1.PipelineSpec{nil, streamPipeline()} {
		t.Run(string(mode.EffectiveMode()), func(t *testing.T) {
			backup := testBackupCR()
			backup.Spec.Encryption = ageEncryption()
			backup.Spec.Pipeline = mode
			c, _, updated := reconcilePVCBackup(t, backup, ageBackupSecret(testBackupSecret()))
			assertBackupReady(t, updated)

			cj := &batchv1.CronJob{}
			if err := c.Get(context.Background(), client.ObjectKey{Namespace: backup.Namespace, Name: resources.BackupOwnedName(backup)}, cj); err != nil {
				t.Fatal(err)
			}
			for _, v := range cj.Spec.JobTemplate.Spec.Template.Spec.Volumes {
				if v.Secret == nil {
					continue
				}
				for _, it := range v.Secret.Items {
					if it.Key == "gpg_passphrase" || it.Key == "age_identity" {
						t.Errorf("age backup projects %q; it must hold only the public key", it.Key)
					}
				}
			}
		})
	}
}

func TestBackupReconcile_AgeMissingRecipientIsInvalid(t *testing.T) {
	backup := testBackupCR()
	backup.Spec.Encryption = ageEncryption()
	// gpg_passphrase alone does not satisfy an age Backup.
	_, _, updated := reconcilePVCBackup(t, backup, testBackupSecret())
	assertSecretInvalid(t, updated.Status.Conditions, updated.Status.Phase, karkivev1alpha1.BackupPhaseError, "age_recipient")
}

func TestBackupReconcile_PVCAgeNeedsOnlyRecipient(t *testing.T) {
	backup := testPVCBackupCR()
	backup.Spec.Encryption = ageEncryption()
	_, _, updated := reconcilePVCBackup(t, backup, ageBackupSecret(testPVCBackupSecret()), testSourcePVC())
	assertBackupReady(t, updated)
}

func TestBackupReconcile_GPGDefaultStillRequiresPassphrase(t *testing.T) {
	backup := testBackupCR()
	secret := testBackupSecret()
	delete(secret.Data, "gpg_passphrase")
	secret.Data["age_recipient"] = []byte(testAgeRecipient)
	_, _, updated := reconcilePVCBackup(t, backup, secret)
	assertSecretInvalid(t, updated.Status.Conditions, updated.Status.Phase, karkivev1alpha1.BackupPhaseError, "gpg_passphrase")
}

func testRestoreCR() *karkivev1alpha1.Restore {
	return &karkivev1alpha1.Restore{
		ObjectMeta: metav1.ObjectMeta{Name: "app-postgres", Namespace: "backup", Generation: 1},
		Spec: karkivev1alpha1.RestoreSpec{
			Engine:   karkivev1alpha1.EnginePostgres,
			Schedule: "30 2 * * *",
			Database: karkivev1alpha1.DatabaseSpec{Host: "postgres.example.svc.cluster.local", Name: "app"},
			S3: karkivev1alpha1.S3Spec{
				Endpoint: "https://s3.example.com",
				Bucket:   "backups",
				Path:     "app/pgdump",
			},
			SecretRef:      corev1.LocalObjectReference{Name: "restore-creds"},
			PostgresSecret: &karkivev1alpha1.SecretKeySelector{Name: "postgres"},
			Persistence:    &karkivev1alpha1.PersistenceSpec{Enabled: ptr.To(false)},
		},
	}
}

func reconcileRestore(t *testing.T, restore *karkivev1alpha1.Restore, jobSecretData map[string][]byte) (client.Client, *karkivev1alpha1.Restore) {
	t.Helper()
	scheme := testScheme(t)
	jobSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "restore-creds", Namespace: "backup"},
		Data:       jobSecretData,
	}
	pgSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "postgres", Namespace: "backup"},
		Data:       map[string][]byte{"username": []byte("postgres"), "password": []byte("secret")},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(restore, jobSecret, pgSecret).
		WithStatusSubresource(&karkivev1alpha1.Restore{}).
		Build()
	r := &RestoreReconciler{Client: c, Scheme: scheme, Recorder: record.NewFakeRecorder(16)}
	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: restore.Name, Namespace: restore.Namespace},
	}); err != nil {
		t.Fatal(err)
	}
	updated := &karkivev1alpha1.Restore{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(restore), updated); err != nil {
		t.Fatal(err)
	}
	return c, updated
}

func TestRestoreReconcile_AgeNeedsIdentityNotPassphrase(t *testing.T) {
	for _, mode := range []*karkivev1alpha1.PipelineSpec{nil, streamPipeline()} {
		t.Run(string(mode.EffectiveMode()), func(t *testing.T) {
			restore := testRestoreCR()
			restore.Spec.Encryption = ageEncryption()
			restore.Spec.Pipeline = mode
			c, updated := reconcileRestore(t, restore, map[string][]byte{
				"s3_access_key": []byte("ak"),
				"s3_secret_key": []byte("sk"),
				"age_identity":  []byte(testAgeIdentity),
			})
			if updated.Status.Phase != karkivev1alpha1.RestorePhaseReady {
				t.Fatalf("phase=%q conditions=%v", updated.Status.Phase, updated.Status.Conditions)
			}
			cm := &corev1.ConfigMap{}
			if err := c.Get(context.Background(), client.ObjectKey{Namespace: restore.Namespace, Name: resources.RestoreOwnedName(restore)}, cm); err != nil {
				t.Fatal(err)
			}
			if cm.Data["ENCRYPTION_METHOD"] != "age" {
				t.Errorf("ENCRYPTION_METHOD=%q", cm.Data["ENCRYPTION_METHOD"])
			}
		})
	}
}

func TestRestoreReconcile_AgeMissingIdentityIsInvalid(t *testing.T) {
	restore := testRestoreCR()
	restore.Spec.Encryption = ageEncryption()
	_, updated := reconcileRestore(t, restore, map[string][]byte{
		"s3_access_key":  []byte("ak"),
		"s3_secret_key":  []byte("sk"),
		"gpg_passphrase": []byte("pgp"),
	})
	assertSecretInvalid(t, updated.Status.Conditions, updated.Status.Phase, karkivev1alpha1.RestorePhaseError, "age_identity")
}

func TestRestoreReconcile_GPGDefaultStillRequiresPassphrase(t *testing.T) {
	_, updated := reconcileRestore(t, testRestoreCR(), map[string][]byte{
		"s3_access_key": []byte("ak"),
		"s3_secret_key": []byte("sk"),
		"age_identity":  []byte(testAgeIdentity),
	})
	assertSecretInvalid(t, updated.Status.Conditions, updated.Status.Phase, karkivev1alpha1.RestorePhaseError, "gpg_passphrase")
}
