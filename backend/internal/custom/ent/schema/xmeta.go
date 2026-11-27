// Package schema holds the ent schemas for the fork's custom tables (x_*).
// These are generated into internal/custom/ent, a client separate from upstream's ent
// (.fork/PLAN.md R2). Keep each schema in sync with the SQL in internal/custom/migrations.
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

// XMeta is fork bookkeeping (key/value), table x_meta.
type XMeta struct {
	ent.Schema
}

func (XMeta) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "x_meta"}}
}

func (XMeta) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").StorageKey("key").NotEmpty().Immutable(),
		field.String("value").Default(""),
	}
}
