package resources

import (
	corev1 "k8s.io/api/core/v1"

	karkivev1alpha1 "github.com/mahdidarabi/KArkive/api/v1alpha1"
	"github.com/mahdidarabi/KArkive/internal/config"
)

const (
	// Secret keys of the encryption material in spec.secretRef.
	SecretKeyGPGPassphrase = "gpg_passphrase"
	SecretKeyAgeRecipient  = "age_recipient"
	SecretKeyAgeIdentity   = "age_identity"

	volumeAge = "age-credentials"
	mountAge  = "/run/secrets/age"
)

// BackupEncryptionSecretKey is the spec.secretRef key a Backup encrypts with: gpg_passphrase (gpg) or age_recipient (age, public key).
func BackupEncryptionSecretKey(backup *karkivev1alpha1.Backup) string {
	if backup.Spec.Encryption.IsAge() {
		return SecretKeyAgeRecipient
	}
	return SecretKeyGPGPassphrase
}

// RestoreEncryptionSecretKey is the spec.secretRef key a Restore decrypts with: gpg_passphrase (gpg) or age_identity (age, private key).
func RestoreEncryptionSecretKey(restore *karkivev1alpha1.Restore) string {
	if restore.Spec.Encryption.IsAge() {
		return SecretKeyAgeIdentity
	}
	return SecretKeyGPGPassphrase
}

// encryptionVolumeName and encryptionMountPath keep the gpg volume (gpg-credentials at /run/secrets/gpg) unchanged; age uses its own.
func encryptionVolumeName(enc *karkivev1alpha1.EncryptionSpec) string {
	if enc.IsAge() {
		return volumeAge
	}
	return volumeGPG
}

func encryptionMountPath(enc *karkivev1alpha1.EncryptionSpec) string {
	if enc.IsAge() {
		return mountAge
	}
	return mountGPG
}

// encryptionVolume projects only the one key the pipeline needs from spec.secretRef.
func encryptionVolume(enc *karkivev1alpha1.EncryptionSpec, secret, key string) corev1.Volume {
	return corev1.Volume{
		Name: encryptionVolumeName(enc),
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: secret,
				Items:      []corev1.KeyToPath{{Key: key, Path: key}},
			},
		},
	}
}

func encryptionVolumeMount(enc *karkivev1alpha1.EncryptionSpec) corev1.VolumeMount {
	return corev1.VolumeMount{Name: encryptionVolumeName(enc), MountPath: encryptionMountPath(enc), ReadOnly: true}
}

// encryptionImage is the Stage-mode encrypt/decrypt image: GnuPG for gpg, Age for age.
func encryptionImage(enc *karkivev1alpha1.EncryptionSpec, images *karkivev1alpha1.ImageSet, cfg config.Config) (string, corev1.PullPolicy) {
	if enc.IsAge() {
		return pipelineImage(images, func(i *karkivev1alpha1.ImageSet) *karkivev1alpha1.ImageSpec {
			return i.Age
		}, cfg.AgeImage, config.DefaultAgeImage)
	}
	return gnuPGImage(images, cfg)
}

// encryptionEnv is merged into the ConfigMap. gpg (the default) adds nothing, so existing ConfigMaps stay as they were.
func encryptionEnv(enc *karkivev1alpha1.EncryptionSpec) map[string]string {
	if !enc.IsAge() {
		return nil
	}
	return map[string]string{"ENCRYPTION_METHOD": string(karkivev1alpha1.EncryptionMethodAge)}
}
