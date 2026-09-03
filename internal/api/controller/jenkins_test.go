package controller

import (
	"testing"

	"github.com/go-ree/ares/internal/entity"
)

func TestTaskBuildLogReferenceUsesPersistedTaskIdentity(t *testing.T) {
	task := entity.TaskRecord{
		TaskId: 17, CiJobName: "folder/build", CiBuildId: 42,
		CdJobName: "folder/deploy", CdBuildId: 73,
		JenkinsAddress: "https://jenkins.example/",
	}
	ci, err := taskBuildLogReference(task, "ci", 100, "https://jenkins.example")
	if err != nil {
		t.Fatal(err)
	}
	if ci.JobName != task.CiJobName || ci.BuildId != task.CiBuildId || ci.Start != 100 {
		t.Fatalf("CI reference = %#v", ci)
	}
	cd, err := taskBuildLogReference(task, "cd", 0, "https://jenkins.example/")
	if err != nil {
		t.Fatal(err)
	}
	if cd.JobName != task.CdJobName || cd.BuildId != task.CdBuildId {
		t.Fatalf("CD reference = %#v", cd)
	}
	if _, err := taskBuildLogReference(task, "other", 0, "https://jenkins.example"); err == nil {
		t.Fatal("arbitrary log type should be rejected")
	}
	if _, err := taskBuildLogReference(entity.TaskRecord{TaskId: 18}, "ci", 0, "https://jenkins.example"); err == nil {
		t.Fatal("task without a persisted build reference should be rejected")
	}
	if _, err := taskBuildLogReference(task, "ci", 0, "https://jenkins-new.example"); err == nil {
		t.Fatal("a different Jenkins instance should be rejected")
	}
}
