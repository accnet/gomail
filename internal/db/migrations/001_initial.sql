-- Reference migration for the V1 schema. The app also runs GORM AutoMigrate at startup.
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Tables are represented in internal/db/models.go. Use AutoMigrate for dev/self-hosted V1,
-- then freeze generated SQL before production upgrades.
