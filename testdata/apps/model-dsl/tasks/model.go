package tasks

import (
	"time"

	"scenery.sh/model"
	"scenery.sh/page"
)

const statusField = "Status"

//scenery:model
type Task struct {
	ID        string    `db:"id"`
	Title     string    `db:"title"`
	Status    string    `db:"status"`
	ProjectID string    `db:"project_id"`
	AgeDays   int       `scenery:"column=age_days"`
	CreatedAt time.Time `db:"created_at"`
}

var taskEntity = model.Entity[Task](
	model.Table("tasks"),
	model.Generate(model.ActionList, model.ActionGet, model.ActionCreate, model.ActionUpdate, model.ActionDelete),
	model.Disable(model.ActionDelete),
	model.Field(statusField, model.EnumValues("todo", "doing", "done"), model.Filterable()),
	model.Field("ProjectID", model.Relationship()),
	model.Field("AgeDays", model.Computed()),
)

//scenery:page
var TaskList = page.Collection[Task]{
	Route:   "/tasks",
	Title:   "Tasks",
	Columns: []string{"Title", "Status", "CreatedAt"},
	Slots: []page.ComponentRef{
		page.Component("TaskStatusBadge"),
	},
}

var _ = taskEntity
