package model

import "time"

type SchemaMigration struct {
	ID        int64     `gorm:"autoIncrement;primaryKey"`
	Version   string    `gorm:"type:varchar(64);uniqueIndex;not null"`
	AppliedAt time.Time `gorm:"type:timestamptz;not null;default:now()"`
}

func (SchemaMigration) TableName() string {
	return "schema_migrations"
}
