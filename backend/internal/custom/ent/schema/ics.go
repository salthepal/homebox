package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// XIcsFeed is a group's ICS feed token (table x_ics_feeds), keyed by group_id. Only the
// SHA-256 of the token is stored.
type XIcsFeed struct {
	ent.Schema
}

func (XIcsFeed) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "x_ics_feeds"}}
}

func (XIcsFeed) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).StorageKey("group_id").Immutable(),
		field.String("token_hash").Unique().NotEmpty().Sensitive(),
		field.UUID("created_by", uuid.UUID{}),
		field.Time("created_at").Default(time.Now),
		field.Time("rotated_at").Optional().Nillable(),
	}
}
