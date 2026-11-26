package jobstatus

import (
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	karkivev1alpha1 "github.com/mahdidarabi/KArkive/api/v1alpha1"
	"github.com/mahdidarabi/KArkive/internal/resources"
)

func TestSummarizeFailedJob(t *testing.T) {
	start := metav1.NewTime(time.Unix(1_700_000_000, 0))
	failedAt := metav1.NewTime(start.Add(45 * time.Second))
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "karkive-app-1", Namespace: "ns"},
		Status: batchv1.JobStatus{
			Failed:    1,
			StartTime: &start,
			Conditions: []batchv1.JobCondition{{
				Type:               batchv1.JobFailed,
				Status:             corev1.ConditionTrue,
				Reason:             "BackoffLimitExceeded",
				Message:            "Job has reached the specified backoff limit",
				LastTransitionTime: failedAt,
			}},
		},
	}
	got := Summarize(job)
	if got.Name != "karkive-app-1" {
		t.Errorf("name=%q", got.Name)
	}
	if got.Outcome != karkivev1alpha1.LastJobOutcomeFailed {
		t.Errorf("outcome=%q", got.Outcome)
	}
	if got.Reason != "BackoffLimitExceeded" {
		t.Errorf("reason=%q", got.Reason)
	}
	if got.Message == "" {
		t.Error("expected message")
	}
	if got.CompletionTime == nil || !got.CompletionTime.Equal(&failedAt) {
		t.Errorf("completion=%v", got.CompletionTime)
	}
}

func TestLastFinishedPicksLatest(t *testing.T) {
	t1 := metav1.NewTime(time.Unix(1_700_000_000, 0))
	t2 := metav1.NewTime(t1.Add(time.Hour))
	jobs := []batchv1.Job{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "old",
				Namespace: "ns",
				Labels: map[string]string{
					resources.LabelKind:       resources.KindBackup,
					resources.LabelBackupName: "app",
				},
			},
			Status: batchv1.JobStatus{CompletionTime: &t1, Succeeded: 1},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "new",
				Namespace: "ns",
				Labels: map[string]string{
					resources.LabelKind:       resources.KindBackup,
					resources.LabelBackupName: "app",
				},
			},
			Status: batchv1.JobStatus{CompletionTime: &t2, Succeeded: 1},
		},
	}
	got := LastFinished(jobs, "ns", resources.LabelBackupName, "app", resources.KindBackup)
	if got == nil || got.Name != "new" {
		t.Fatalf("got %+v", got)
	}
}

func TestFailedUsesJobResultNotPodAttempts(t *testing.T) {
	cond := func(tp batchv1.JobConditionType) []batchv1.JobCondition {
		return []batchv1.JobCondition{{Type: tp, Status: corev1.ConditionTrue}}
	}
	cases := []struct {
		name string
		st   batchv1.JobStatus
		want bool
	}{
		{"succeeded on retry", batchv1.JobStatus{Failed: 1, Succeeded: 1, Conditions: cond(batchv1.JobComplete)}, false},
		{"succeeded first try", batchv1.JobStatus{Succeeded: 1, Conditions: cond(batchv1.JobComplete)}, false},
		{"backoff limit exceeded", batchv1.JobStatus{Failed: 4, Conditions: cond(batchv1.JobFailed)}, true},
		{"failure target before Failed", batchv1.JobStatus{Failed: 2, Conditions: cond(batchv1.JobFailureTarget)}, true},
		{"no conditions, only failed pods", batchv1.JobStatus{Failed: 1}, true},
		{"no conditions, succeeded after a failed pod", batchv1.JobStatus{Failed: 1, Succeeded: 1}, false},
	}
	for _, tc := range cases {
		if got := Failed(&batchv1.Job{Status: tc.st}); got != tc.want {
			t.Errorf("%s: Failed=%v, want %v", tc.name, got, tc.want)
		}
	}
	retried := &batchv1.Job{Status: batchv1.JobStatus{Failed: 1, Succeeded: 1, Conditions: cond(batchv1.JobComplete)}}
	if got := Summarize(retried).Outcome; got != karkivev1alpha1.LastJobOutcomeSucceeded {
		t.Errorf("Summarize outcome=%q, want Succeeded", got)
	}
}
