package db

import (
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func NormalizeSaaSDomainMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case SaaSDomainModeLanding:
		return SaaSDomainModeLanding
	default:
		return SaaSDomainModeApp
	}
}

func GetAppSetting(database *gorm.DB, key string, fallback string) string {
	var setting AppSetting
	if err := database.First(&setting, "key = ?", key).Error; err == nil {
		return setting.Value
	}
	return fallback
}

func SetAppSetting(database *gorm.DB, key string, value string) error {
	setting := AppSetting{Key: key, Value: value}
	return database.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
	}).Create(&setting).Error
}

func GetSaaSDomainMode(database *gorm.DB) string {
	return NormalizeSaaSDomainMode(GetAppSetting(database, AppSettingSaaSDomainMode, SaaSDomainModeApp))
}

func SetSaaSDomainMode(database *gorm.DB, value string) error {
	return SetAppSetting(database, AppSettingSaaSDomainMode, NormalizeSaaSDomainMode(value))
}
