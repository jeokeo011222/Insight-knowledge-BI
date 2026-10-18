package models

import (
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Task maps the tasks table (superset.models.tasks.Task).
type Task struct {
	BaseModel
	UUID          string         `gorm:"column:uuid;type:uuid;unique;not null" json:"uuid"`
	TaskKey       string         `gorm:"column:task_key;not null;index" json:"task_key"`
	TaskType      string         `gorm:"column:task_type;not null;index" json:"task_type"`
	TaskName      string         `gorm:"column:task_name" json:"task_name"`
	Scope         string         `gorm:"column:scope;not null;index;default:private" json:"scope"`
	Status        string         `gorm:"column:status;not null;index;default:pending" json:"status"`
	DedupKey      string         `gorm:"column:dedup_key;not null;uniqueIndex:uq_tasks_dedup_key" json:"dedup_key"`
	StartedAt     *time.Time     `gorm:"column:started_at" json:"started_at"`
	EndedAt       *time.Time     `gorm:"column:ended_at" json:"ended_at"`
	LastHeartbeat *time.Time     `gorm:"column:last_heartbeat;index" json:"last_heartbeat"`
	UserID        *int           `gorm:"column:user_id" json:"user_id"`
	Payload       datatypes.JSON `gorm:"column:payload" json:"payload"`
	Properties    datatypes.JSON `gorm:"column:properties" json:"properties"`
}

// TableName returns the original table name.
func (Task) TableName() string { return "tasks" }

// BeforeCreate generates the UUID when unset.
func (m *Task) BeforeCreate(tx *gorm.DB) error {
	if m.UUID == "" {
		m.UUID = NewUUID()
	}
	return nil
}

// TaskDependency maps the task_dependencies table (superset.models.task_dependencies.TaskDependency).
type TaskDependency struct {
	BaseModel
	TaskID          int `gorm:"column:task_id;not null;uniqueIndex:uq_task_dependencies_task_depends_on" json:"task_id"`
	DependsOnTaskID int `gorm:"column:depends_on_task_id;not null;uniqueIndex:uq_task_dependencies_task_depends_on" json:"depends_on_task_id"`
}

// TableName returns the original table name.
func (TaskDependency) TableName() string { return "task_dependencies" }

// TaskSubscriber maps the task_subscribers table (superset.models.task_subscribers.TaskSubscriber).
type TaskSubscriber struct {
	BaseModel
	TaskID       int       `gorm:"column:task_id;not null;uniqueIndex:uq_task_subscribers_task_user,priority:1;uniqueIndex:uq_task_subscribers_task_guest,priority:1" json:"task_id"`
	UserID       *int      `gorm:"column:user_id;uniqueIndex:uq_task_subscribers_task_user,priority:2" json:"user_id"`
	GuestKey     string    `gorm:"column:guest_key;index;uniqueIndex:uq_task_subscribers_task_guest,priority:2" json:"guest_key"`
	SubscribedAt time.Time `gorm:"column:subscribed_at;not null" json:"subscribed_at"`
}

// TableName returns the original table name.
func (TaskSubscriber) TableName() string { return "task_subscribers" }
