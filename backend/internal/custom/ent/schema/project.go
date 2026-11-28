package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// XProject is a multi-step project (table x_projects). group_id is a plain UUID, not an
// edge: upstream tables are never referenced by foreign key (.fork/PLAN.md R4).
type XProject struct {
	ent.Schema
}

func (XProject) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "x_projects"}}
}

func (XProject) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.UUID("group_id", uuid.UUID{}).Immutable(),
		field.String("name").MaxLen(255).NotEmpty(),
		field.String("description").Default(""),
		field.Enum("status").Values("idea", "planning", "in_progress", "on_hold", "done", "cancelled").Default("idea"),
		field.Int("priority").Default(0).Min(0).Max(3),
		field.Time("target_date").Optional().Nillable().
			SchemaType(map[string]string{dialect.Postgres: "date", dialect.SQLite: "date"}),
		field.Float("budget").Optional().Nillable(),
		field.Time("started_at").Optional().Nillable(),
		field.Time("completed_at").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (XProject) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("steps", XProjectStep.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

func (XProject) Indexes() []ent.Index {
	return []ent.Index{index.Fields("group_id", "status")}
}

// XProjectStep is one ordered step of a project (table x_project_steps). entity_id and
// maintenance_entry_id point at upstream rows by plain UUID.
type XProjectStep struct {
	ent.Schema
}

func (XProjectStep) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "x_project_steps"}}
}

func (XProjectStep) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.UUID("project_id", uuid.UUID{}),
		field.Int("position").Default(0),
		field.String("title").MaxLen(255).NotEmpty(),
		field.String("notes").Default(""),
		field.Enum("status").Values("todo", "doing", "blocked", "done", "skipped").Default("todo"),
		field.Time("due_date").Optional().Nillable().
			SchemaType(map[string]string{dialect.Postgres: "date", dialect.SQLite: "date"}),
		field.Float("est_cost").Optional().Nillable(),
		field.Float("actual_cost").Optional().Nillable(),
		field.String("url").Optional().Nillable(),
		field.UUID("entity_id", uuid.UUID{}).Optional().Nillable(),
		field.Bool("log_as_maintenance").Default(false),
		field.UUID("maintenance_entry_id", uuid.UUID{}).Optional().Nillable(),
		field.Time("completed_at").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (XProjectStep) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("project", XProject.Type).Ref("steps").Field("project_id").Unique().Required(),
	}
}

func (XProjectStep) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id", "position"),
		index.Fields("entity_id"),
	}
}
