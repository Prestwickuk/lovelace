package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestChangeoverStageTransitions(t *testing.T) {
	if !StageGuestDeparting.CanTransitionTo(StageCleaningRequired) {
		t.Fatal("expected GUEST_DEPARTING -> CLEANING_REQUIRED to be permitted")
	}
	if !StageCleaningRequired.CanTransitionTo(StageCleaningInProgress) {
		t.Fatal("expected CLEANING_REQUIRED -> CLEANING_IN_PROGRESS to be permitted")
	}
	if StageCleaningRequired.CanTransitionTo(StageReady) {
		t.Fatal("expected skipping cleaning/inspection directly from CLEANING_REQUIRED -> READY to be rejected")
	}
	if !StageCleaningInProgress.CanTransitionTo(StageInspectionRequired) {
		t.Fatal("expected CLEANING_IN_PROGRESS -> INSPECTION_REQUIRED to be permitted")
	}
	if !StageInspectionRequired.CanTransitionTo(StageReady) {
		t.Fatal("expected INSPECTION_REQUIRED -> READY to be permitted")
	}
	if !StageInspectionRequired.CanTransitionTo(StageCleaningRequired) {
		t.Fatal("expected failed inspection INSPECTION_REQUIRED -> CLEANING_REQUIRED to be permitted")
	}
	if !StageReady.CanTransitionTo(StageCleaningRequired) {
		t.Fatal("expected READY -> CLEANING_REQUIRED re-queue to be permitted")
	}
}

func TestHousekeepingTaskValidate(t *testing.T) {
	task := HousekeepingTask{
		PropertyID:    uuid.New(),
		UnitID:        uuid.New(),
		ScheduledDate: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := task.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if task.TaskType != TaskTypeChangeoverClean || task.Stage != StageCleaningRequired || task.Priority != TaskPriorityNormal {
		t.Fatalf("expected sensible defaults on task: %+v", task)
	}
	if task.Title == "" {
		t.Fatal("expected default title to be populated")
	}
}
