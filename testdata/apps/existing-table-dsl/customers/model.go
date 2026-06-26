package customers

import (
	"time"

	"scenery.sh/model"
	"scenery.sh/page"
)

//scenery:service
type Service struct{}

//scenery:model
type Customer struct {
	ID         string    `db:"id"`
	Email      string    `db:"email"`
	Name       string    `db:"full_name"`
	PlanStatus string    `db:"plan_status"`
	CreatedAt  time.Time `db:"created_at"`
}

var customerEntity = model.Entity[Customer](
	model.ExistingTable("legacy", "customers"),
	model.Generate(model.ActionList, model.ActionGet),
	model.Field("PlanStatus", model.Filterable()),
)

//scenery:page
var CustomerList = page.Collection[Customer]{
	Route:   "/customers",
	Title:   "Customers",
	Columns: []string{"Email", "Name", "PlanStatus", "CreatedAt"},
	ColumnDisplays: []page.ColumnDisplayRef{
		page.Column("PlanStatus", page.DisplayBadge),
		page.Column("CreatedAt", page.DisplayDateTime),
	},
	Filters: []page.FilterRef{
		page.Filter("PlanStatus", page.NotEqual, "cancelled"),
	},
	Sorts: []page.SortRef{
		page.Sort("CreatedAt", page.Desc),
	},
}

var _ = customerEntity
