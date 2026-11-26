package resources

import (
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	karkivev1alpha1 "github.com/mahdidarabi/KArkive/api/v1alpha1"
	"github.com/mahdidarabi/KArkive/internal/config"
)

var (
	ageEncryption = &karkivev1alpha1.EncryptionSpec{Method: karkivev1alpha1.EncryptionMethodAge}
	streamMode    = &karkivev1alpha1.PipelineSpec{Mode: karkivev1alpha1.PipelineModeStream}
	// stageMode opts a fixture into the per-stage pipeline (Stream is the default).
	stageMode = &karkivev1alpha1.PipelineSpec{Mode: karkivev1alpha1.PipelineModeStage}
)

func podSpec(cj *batchv1.CronJob) corev1.PodSpec {
	return cj.Spec.JobTemplate.Spec.Template.Spec
}

func container(t *testing.T, cj *batchv1.CronJob, name string) corev1.Container {
	t.Helper()
	for _, c := range podSpec(cj).Containers {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no container %q in %v", name, containerNames(cj))
	return corev1.Container{}
}

func volume(t *testing.T, cj *batchv1.CronJob, name string) *corev1.Volume {
	t.Helper()
	for i, v := range podSpec(cj).Volumes {
		if v.Name == name {
			return &podSpec(cj).Volumes[i]
		}
	}
	return nil
}

func hasMount(c corev1.Container, name, path string) bool {
	for _, m := range c.VolumeMounts {
		if m.Name == name && m.MountPath == path && m.ReadOnly {
			return true
		}
	}
	return false
}

// secretItems is the only keys projected from spec.secretRef into the encryption volume.
func secretItems(t *testing.T, v *corev1.Volume, secret string) []string {
	t.Helper()
	if v == nil || v.Secret == nil {
		t.Fatalf("encryption volume missing or not a Secret: %v", v)
	}
	if v.Secret.SecretName != secret {
		t.Errorf("secretName=%q, want %q", v.Secret.SecretName, secret)
	}
	keys := make([]string, 0, len(v.Secret.Items))
	for _, it := range v.Secret.Items {
		if it.Key != it.Path {
			t.Errorf("item %q mounted as %q", it.Key, it.Path)
		}
		keys = append(keys, it.Key)
	}
	return keys
}

func assertOnlyKey(t *testing.T, keys []string, want string) {
	t.Helper()
	if len(keys) != 1 || keys[0] != want {
		t.Errorf("projected keys=%v, want [%s]", keys, want)
	}
}

func TestEncryptionDefaultsToGPG(t *testing.T) {
	var nilSpec *karkivev1alpha1.EncryptionSpec
	for _, e := range []*karkivev1alpha1.EncryptionSpec{nilSpec, {}, {Method: karkivev1alpha1.EncryptionMethodGPG}} {
		if e.EffectiveMethod() != karkivev1alpha1.EncryptionMethodGPG || e.IsAge() || e.Extension() != "gpg" {
			t.Errorf("%v: method=%q ext=%q", e, e.EffectiveMethod(), e.Extension())
		}
	}
	if ageEncryption.Extension() != "age" || !ageEncryption.IsAge() {
		t.Errorf("age ext=%q", ageEncryption.Extension())
	}
	if BackupEncryptionSecretKey(testBackup()) != "gpg_passphrase" || RestoreEncryptionSecretKey(testRestore()) != "gpg_passphrase" {
		t.Error("gpg default must keep the gpg_passphrase key")
	}
}

// Stage with no spec.encryption must render exactly what it did before age existed.
func TestStageGPGDefaultUnchanged(t *testing.T) {
	backup := testBackup()
	var bcj batchv1.CronJob
	MutateBackupCronJob(&bcj, backup, config.Config{})
	enc := container(t, &bcj, "encrypt")
	if enc.Image != config.DefaultGnuPGImage {
		t.Errorf("encrypt image=%s", enc.Image)
	}
	if !hasMount(enc, "gpg-credentials", "/run/secrets/gpg") {
		t.Errorf("encrypt mounts=%v", enc.VolumeMounts)
	}
	assertOnlyKey(t, secretItems(t, volume(t, &bcj, "gpg-credentials"), "backup-app-postgres"), "gpg_passphrase")
	if volume(t, &bcj, "age-credentials") != nil {
		t.Error("gpg backup must not mount an age volume")
	}
	var bcm corev1.ConfigMap
	if err := MutateBackupConfigMap(&bcm, backup, config.Config{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := bcm.Data["ENCRYPTION_METHOD"]; ok {
		t.Errorf("gpg default must not add ENCRYPTION_METHOD: %v", bcm.Data)
	}

	restore := testRestore()
	var rcj batchv1.CronJob
	MutateRestoreCronJob(&rcj, restore, config.Config{})
	dec := container(t, &rcj, "decrypt")
	if dec.Image != config.DefaultGnuPGImage || !hasMount(dec, "gpg-credentials", "/run/secrets/gpg") {
		t.Errorf("decrypt image=%s mounts=%v", dec.Image, dec.VolumeMounts)
	}
	assertOnlyKey(t, secretItems(t, volume(t, &rcj, "gpg-credentials"), restore.Spec.SecretRef.Name), "gpg_passphrase")
	var rcm corev1.ConfigMap
	if err := MutateRestoreConfigMap(&rcm, restore, config.Config{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := rcm.Data["ENCRYPTION_METHOD"]; ok {
		t.Errorf("gpg default must not add ENCRYPTION_METHOD: %v", rcm.Data)
	}
}

func TestStageAgeBackup(t *testing.T) {
	for _, backup := range []*karkivev1alpha1.Backup{testBackup(), testMariaBackup(), testRedisBackup(), testPVCBackup()} {
		backup.Spec.Encryption = ageEncryption
		t.Run(string(EffectiveEngine(backup.Spec.Engine)), func(t *testing.T) {
			var cj batchv1.CronJob
			MutateBackupCronJob(&cj, backup, config.Config{})
			enc := container(t, &cj, "encrypt")
			if enc.Image != config.DefaultAgeImage {
				t.Errorf("encrypt image=%s, want %s", enc.Image, config.DefaultAgeImage)
			}
			if !hasMount(enc, "age-credentials", "/run/secrets/age") {
				t.Errorf("encrypt mounts=%v", enc.VolumeMounts)
			}
			// Public key only; no gpg passphrase volume.
			assertOnlyKey(t, secretItems(t, volume(t, &cj, "age-credentials"), backup.Spec.SecretRef.Name), "age_recipient")
			if volume(t, &cj, "gpg-credentials") != nil {
				t.Error("age backup must not mount gpg_passphrase")
			}
			var cm corev1.ConfigMap
			if err := MutateBackupConfigMap(&cm, backup, config.Config{}); err != nil {
				t.Fatal(err)
			}
			if cm.Data["ENCRYPTION_METHOD"] != "age" {
				t.Errorf("ENCRYPTION_METHOD=%q", cm.Data["ENCRYPTION_METHOD"])
			}
		})
	}
}

func TestStageAgeRestore(t *testing.T) {
	for _, restore := range []*karkivev1alpha1.Restore{testRestore(), testMariaRestore(), testRedisRestore()} {
		restore.Spec.Encryption = ageEncryption
		t.Run(string(EffectiveEngine(restore.Spec.Engine)), func(t *testing.T) {
			var cj batchv1.CronJob
			MutateRestoreCronJob(&cj, restore, config.Config{})
			dec := container(t, &cj, "decrypt")
			if dec.Image != config.DefaultAgeImage || !hasMount(dec, "age-credentials", "/run/secrets/age") {
				t.Errorf("decrypt image=%s mounts=%v", dec.Image, dec.VolumeMounts)
			}
			assertOnlyKey(t, secretItems(t, volume(t, &cj, "age-credentials"), restore.Spec.SecretRef.Name), "age_identity")
			if volume(t, &cj, "gpg-credentials") != nil {
				t.Error("age restore must not mount gpg_passphrase")
			}
			var cm corev1.ConfigMap
			if err := MutateRestoreConfigMap(&cm, restore, config.Config{}); err != nil {
				t.Fatal(err)
			}
			if cm.Data["ENCRYPTION_METHOD"] != "age" {
				t.Errorf("ENCRYPTION_METHOD=%q", cm.Data["ENCRYPTION_METHOD"])
			}
		})
	}
}

func TestStageAgeImageOverrides(t *testing.T) {
	backup := testBackup()
	backup.Spec.Encryption = ageEncryption
	var cj batchv1.CronJob
	MutateBackupCronJob(&cj, backup, config.Config{AgeImage: "registry/age:op"})
	if img := container(t, &cj, "encrypt").Image; img != "registry/age:op" {
		t.Errorf("operator --age-image ignored: %s", img)
	}
	backup.Spec.Images = &karkivev1alpha1.ImageSet{
		Age:   &karkivev1alpha1.ImageSpec{Image: "registry/age:cr", PullPolicy: corev1.PullAlways},
		GnuPG: &karkivev1alpha1.ImageSpec{Image: "registry/gpg:cr"},
	}
	MutateBackupCronJob(&cj, backup, config.Config{AgeImage: "registry/age:op"})
	enc := container(t, &cj, "encrypt")
	if enc.Image != "registry/age:cr" || enc.ImagePullPolicy != corev1.PullAlways {
		t.Errorf("spec.images.age ignored: %s %s", enc.Image, enc.ImagePullPolicy)
	}
	// spec.images.gnupg still drives the gpg path.
	backup.Spec.Encryption = nil
	MutateBackupCronJob(&cj, backup, config.Config{AgeImage: "registry/age:op"})
	if img := container(t, &cj, "encrypt").Image; img != "registry/gpg:cr" {
		t.Errorf("gpg encrypt image=%s", img)
	}
}

func TestStreamEncryptionEnvAndMounts(t *testing.T) {
	cases := []struct {
		name               string
		enc                *karkivev1alpha1.EncryptionSpec
		vol, mount         string
		backupKey, rKey    string
		backupVal, restVal string
	}{
		{
			name: "gpg default", enc: nil,
			vol: "gpg-credentials", mount: "/run/secrets/gpg",
			backupKey: "GPG_PASSFILE", rKey: "GPG_PASSFILE",
			backupVal: "/run/secrets/gpg/gpg_passphrase", restVal: "/run/secrets/gpg/gpg_passphrase",
		},
		{
			name: "age", enc: ageEncryption,
			vol: "age-credentials", mount: "/run/secrets/age",
			backupKey: "AGE_RECIPIENTS_FILE", rKey: "AGE_IDENTITY_FILE",
			backupVal: "/run/secrets/age/age_recipient", restVal: "/run/secrets/age/age_identity",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, backup := range []*karkivev1alpha1.Backup{testBackup(), testPVCBackup()} {
				backup.Spec.Pipeline = streamMode
				backup.Spec.Encryption = tc.enc
				var cj batchv1.CronJob
				MutateBackupCronJob(&cj, backup, config.Config{})
				if !hasMount(container(t, &cj, "stream"), tc.vol, tc.mount) {
					t.Errorf("%s stream mounts=%v", backup.Spec.Engine, container(t, &cj, "stream").VolumeMounts)
				}
				want := "gpg_passphrase"
				if tc.enc.IsAge() {
					want = "age_recipient"
				}
				assertOnlyKey(t, secretItems(t, volume(t, &cj, tc.vol), backup.Spec.SecretRef.Name), want)
				var cm corev1.ConfigMap
				if err := MutateBackupConfigMap(&cm, backup, config.Config{}); err != nil {
					t.Fatal(err)
				}
				if cm.Data[tc.backupKey] != tc.backupVal {
					t.Errorf("%s=%q, want %q", tc.backupKey, cm.Data[tc.backupKey], tc.backupVal)
				}
				if tc.enc.IsAge() {
					if _, ok := cm.Data["GPG_PASSFILE"]; ok || cm.Data["ENCRYPTION_METHOD"] != "age" {
						t.Errorf("age stream backup env: %v", cm.Data)
					}
				} else if _, ok := cm.Data["ENCRYPTION_METHOD"]; ok {
					t.Errorf("gpg stream backup env: %v", cm.Data)
				}
			}

			restore := testRestore()
			restore.Spec.Pipeline = streamMode
			restore.Spec.Encryption = tc.enc
			var cj batchv1.CronJob
			MutateRestoreCronJob(&cj, restore, config.Config{})
			if !hasMount(container(t, &cj, "stream"), tc.vol, tc.mount) {
				t.Errorf("restore stream mounts=%v", container(t, &cj, "stream").VolumeMounts)
			}
			want := "gpg_passphrase"
			if tc.enc.IsAge() {
				want = "age_identity"
			}
			assertOnlyKey(t, secretItems(t, volume(t, &cj, tc.vol), restore.Spec.SecretRef.Name), want)
			var cm corev1.ConfigMap
			if err := MutateRestoreConfigMap(&cm, restore, config.Config{}); err != nil {
				t.Fatal(err)
			}
			if cm.Data[tc.rKey] != tc.restVal {
				t.Errorf("%s=%q, want %q", tc.rKey, cm.Data[tc.rKey], tc.restVal)
			}
			if tc.enc.IsAge() {
				if _, ok := cm.Data["GPG_PASSFILE"]; ok {
					t.Errorf("age stream restore env: %v", cm.Data)
				}
			}
		})
	}
}
