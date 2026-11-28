package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// XGcalConnection is a group's Google Calendar connection (table x_gcal_connections),
// keyed by group_id. The refresh token is stored AES-GCM encrypted.
type XGcalConnection struct {
	ent.Schema
}

func (XGcalConnection) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "x_gcal_connections"}}
}

func (XGcalConnection) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).StorageKey("group_id").Immutable(),
		field.UUID("connected_by", uuid.UUID{}),
		field.String("google_email").Default(""),
		field.String("calendar_id").Default(""),
		field.String("calendar_summary").Default(""),
		field.Bytes("enc_refresh_token").Sensitive(),
		field.Bool("include_completed").Default(true),
		field.Bool("include_project_steps").Default(true),
		field.Int("reminder_minutes").Default(1440),
		field.Bool("broken").Default(false),
		field.Time("last_sync_at").Optional().Nillable(),
		field.String("last_error").Default(""),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// XGcalLink maps one Homebox source row (maintenance entry or project step) to the Google
// event created for it (table x_gcal_links).
type XGcalLink struct {
	ent.Schema
}

func (XGcalLink) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "x_gcal_links"}}
}

func (XGcalLink) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.UUID("group_id", uuid.UUID{}).Immutable(),
		field.Enum("source_type").Values("maintenance", "project_step").Immutable(),
		field.UUID("source_id", uuid.UUID{}).Immutable(),
		field.String("event_id").NotEmpty(),
		field.String("content_hash").Default(""),
		field.Time("synced_at").Default(time.Now),
	}
}

func (XGcalLink) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("group_id", "source_type", "source_id").Unique(),
		index.Fields("group_id", "event_id"),
	}
}
