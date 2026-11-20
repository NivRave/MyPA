package db

import (
	"fmt"
	"github.com/nivik/mypa/internal/identity"
	"log/slog"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Client handles database operations.
type Client struct {
	DB *gorm.DB
}

// Open connects to PostgreSQL with a tuned pool and verifies connectivity. It does NOT change the
// schema: production applies versioned migrations (Migrate / `orchestrator -migrate-only`, P4-3)
// and checks them with EnsureMigrated.
func Open(dsn string) (*Client, error) {
	if dsn == "" {
		return nil, fmt.Errorf("database URL is empty")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: newSQLLogger()})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Configure underlying sql.DB connection pool sizing
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve generic database object: %w", err)
	}

	sqlDB.SetMaxOpenConns(25)                  // Max 25 active connections per service
	sqlDB.SetMaxIdleConns(10)                  // Keep 10 warm idle connections ready
	sqlDB.SetConnMaxLifetime(30 * time.Minute) // Periodically recycle connections
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)  // Reclaim idle connections

	// Verify database connectivity (health check / fail-fast)
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("database ping failed: %w", err)
	}
	slog.Info("connected to postgres", "max_open_conns", 25, "max_idle_conns", 10)
	return &Client{DB: db}, nil
}

// slowSQLThreshold is the duration above which a successful query is logged at WARN.
const slowSQLThreshold = 200 * time.Millisecond

// newSQLLogger routes GORM through the process-wide slog handler (JSON, same as every other line)
// instead of GORM's default coloured stdout logger (BL-2):
//   - ParameterizedQueries: SQL is logged with $1 placeholders, never the bound values — those are
//     phone numbers, chat IDs and user content (P3-3: no personal data in logs).
//   - IgnoreRecordNotFoundError: a miss is normal control flow, not an error.
//   - Warn level: only failed and slow (> slowSQLThreshold) statements are logged.
func newSQLLogger() gormlogger.Interface {
	return gormlogger.NewSlogLogger(slog.Default(), gormlogger.Config{
		SlowThreshold:             slowSQLThreshold,
		LogLevel:                  gormlogger.Warn,
		ParameterizedQueries:      true,
		IgnoreRecordNotFoundError: true,
	})
}

// NewClientAutoMigrate opens a connection and builds the schema with GORM AutoMigrate.
// FOR TESTS AND LOCAL EXPERIMENTS ONLY (P4-3): production uses Open + Migrate. The equivalence
// of the two paths is enforced by TestMigrations_FreshDatabaseMatchesModels.
func NewClientAutoMigrate(dsn string) (*Client, error) {
	c, err := Open(dsn)
	if err != nil {
		return nil, err
	}
	if err := AutoMigrateSchema(c.DB); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// AutoMigrateSchema creates/updates tables from the Go models, applies the custom indexes and the
// legacy-memory data copy — the pre-goose startup behaviour (tests/dev only).
func AutoMigrateSchema(db *gorm.DB) error {
	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
		return fmt.Errorf("failed to create vector extension: %w", err)
	}
	baseModels := []interface{}{
		&models.Household{}, // first: every household_id references it (V4-30)
		&models.AuditSession{},
		&models.AuditEvent{},
		&models.Memory{},
		&models.ScheduledReminder{},
		&models.User{},
		&models.PairingCode{},
		&models.Workflow{},
		// V4-32: people and their channel accounts, memberships and access tokens.
		&models.Person{},
		&models.ChannelAccount{},
		&models.Membership{},
		&models.AccessToken{},
		// V4-35: dashboard sessions.
		&models.Session{},
		// V4-33: messages the assistant starts itself.
		&models.ProactiveDelivery{},
		// V4-03: which embedding model made the stored memory vectors.
		&models.EmbeddingSpace{},
	}
	allModels := append(baseModels, GetRegisteredModels()...)
	if err := db.AutoMigrate(allModels...); err != nil {
		return fmt.Errorf("failed to migrate database: %w", err)
	}
	if err := ApplyCustomIndexes(db); err != nil {
		return fmt.Errorf("failed to apply custom database indexes: %w", err)
	}
	if err := applyHouseholdForeignKeys(db, allModels); err != nil {
		return err
	}
	if err := applyPersonKeys(db); err != nil {
		return err
	}
	if _, err := MigrateLegacyMemories(db); err != nil {
		return err
	}
	return nil
}

// Ping verifies database liveness.
func (c *Client) Ping() error {
	if c.DB == nil {
		return fmt.Errorf("database client is not initialized")
	}
	sqlDB, err := c.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Ping()
}

// Close gracefully closes the database connection pool.
func (c *Client) Close() error {
	if c.DB == nil {
		return nil
	}
	sqlDB, err := c.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// InsertAuditSession saves a new audit session to the database.
func (c *Client) InsertAuditSession(session models.AuditSession) error {
	result := c.DB.Create(&session)
	if result.Error != nil {
		return fmt.Errorf("failed to insert audit session: %w", result.Error)
	}
	return nil
}

// InsertAuditEvent saves a new audit event to the database.
func (c *Client) InsertAuditEvent(event models.AuditEvent) error {
	result := c.DB.Create(&event)
	if result.Error != nil {
		return fmt.Errorf("failed to insert audit event: %w", result.Error)
	}
	return nil
}

// GetUniqueUsers returns the person key of everyone who has written, once per person (V4-32: a
// person's sessions from several accounts, or from before a merge, count once).
func (c *Client) GetUniqueUsers() ([]string, error) {
	var userIDs []string
	result := c.DB.Raw(`SELECT DISTINCT COALESCE(u.platform_id, a.user_id) FROM audit_sessions a
		LEFT JOIN users u ON u.id = a.person_id WHERE COALESCE(u.platform_id, a.user_id) <> ''`).Scan(&userIDs)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get unique users: %w", result.Error)
	}
	return userIDs, nil
}

// GetLastAuditSessionForUser returns the most recent audit session for a given user.
func (c *Client) GetLastAuditSessionForUser(userID string) (*models.AuditSession, error) {
	var session models.AuditSession
	where, args := personIs("user_id", "person_id", userID)
	result := c.DB.Where(where, args...).Order("start_time desc").First(&session)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get last audit session: %w", result.Error)
	}
	return &session, nil
}

// SaveReminder creates a new scheduled reminder.
func (c *Client) SaveReminder(reminder models.ScheduledReminder) error {
	result := c.DB.Create(&reminder)
	if result.Error != nil {
		return fmt.Errorf("failed to insert reminder: %w", result.Error)
	}
	return nil
}

// GetDueReminders fetches reminders that are due to be sent (due_time <= now) and haven't been sent yet.
func (c *Client) GetDueReminders() ([]models.ScheduledReminder, error) {
	var reminders []models.ScheduledReminder
	// Fetch where due_time is less than or equal to now, and is_sent is false.
	result := c.DB.Where("is_sent = ? AND due_time <= ?", false, "now()").Find(&reminders)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to fetch due reminders: %w", result.Error)
	}
	return reminders, nil
}

// MarkReminderSent marks a scheduled reminder as sent.
func (c *Client) MarkReminderSent(id uint) error {
	result := c.DB.Model(&models.ScheduledReminder{}).Where("id = ?", id).Update("is_sent", true)
	if result.Error != nil {
		return fmt.Errorf("failed to mark reminder as sent: %w", result.Error)
	}
	return nil
}

// GetUser retrieves a user by their platform ID.
func (c *Client) GetUser(platformID string) (*models.User, error) {
	var user models.User
	result := c.DB.Where("platform_id = ?", platformID).First(&user)
	if result.Error != nil {
		return nil, result.Error
	}
	return &user, nil
}

// UpsertUser creates or updates a user, and the person and membership behind it (V4-32). The
// user must carry the household it belongs to (V4-30). A new person's role is user.Role read by
// identity.ParseRole ("admin" is an owner and the operator; empty or unknown is an adult); an
// existing person keeps theirs (V4-35). It knows no channel, so it adds no channel account:
// accounts come from pairing, the bootstrap and linking.
func (c *Client) UpsertUser(user models.User) error {
	if strings.TrimSpace(user.HouseholdID) == "" {
		return fmt.Errorf("failed to upsert user: no household")
	}
	role, operator := entryRole(user.Role)
	user.Role = identity.LegacyRole(role)
	return c.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("platform_id = ?", user.PlatformID).Assign(user).FirstOrCreate(&user).Error; err != nil {
			return fmt.Errorf("failed to upsert user: %w", err)
		}
		return ensurePerson(tx, user, "", role, operator)
	})
}

// entryRole is the role and operator flag of a user given with a stored or configured role: the
// four role names, or "admin" (an owner and the operator) and "family" (an adult) as written
// before V4-35. Empty or unknown is an adult, as "family" was the default.
func entryRole(s string) (identity.Role, bool) {
	role := identity.ParseRole(s)
	if role == "" {
		role = identity.RoleAdult
	}
	return role, strings.EqualFold(strings.TrimSpace(s), "admin")
}

// CreatePairingCode inserts a new pairing code into the scope's household: whoever redeems it
// joins that household (V4-30).
func (c *Client) CreatePairingCode(s identity.Scope, code models.PairingCode) error {
	if err := stamp(s, &code.HouseholdID, &code.FamilyGroup); err != nil {
		return err
	}
	result := c.DB.Create(&code)
	if result.Error != nil {
		return fmt.Errorf("failed to create pairing code: %w", result.Error)
	}
	return nil
}

// GetPairingCode retrieves a valid pairing code. A code left in the holding household (written
// before V4-30 without a family) is never valid: it would admit someone to rows nobody owns.
func (c *Client) GetPairingCode(code string) (*models.PairingCode, error) {
	var pc models.PairingCode
	result := c.DB.Where("code = ? AND is_used = ? AND expires_at > now() AND household_id <> ?", code, false, models.HoldingHouseholdID).First(&pc)
	if result.Error != nil {
		return nil, result.Error // e.g., gorm.ErrRecordNotFound
	}
	return &pc, nil
}

// RedeemPairingCode marks a code as used and creates the user in a transaction, with the person
// behind it, their membership and the channel account the code was typed on (V4-32). The new
// user's household is the code's, read inside the transaction; the caller's value is not trusted.
func (c *Client) RedeemPairingCode(codeID string, user models.User, channelName string) error {
	return c.DB.Transaction(func(tx *gorm.DB) error {
		var pc models.PairingCode
		if err := tx.Where("id = ?", codeID).First(&pc).Error; err != nil {
			return err
		}
		if pc.HouseholdID == "" || pc.HouseholdID == models.HoldingHouseholdID {
			return fmt.Errorf("pairing code has no household")
		}
		// V4-35: a code gives adult or child, never owner, whatever the caller passed; a guest code
		// pairs nobody until sharing by name exists (BL-46).
		role, ok := identity.ParseInviteRole(pc.Role)
		if !ok {
			return fmt.Errorf("pairing code has no valid role")
		}
		user.HouseholdID = pc.HouseholdID
		user.FamilyGroup = pc.FamilyGroup
		user.Role = identity.LegacyRole(role)
		// Mark code as used
		if err := tx.Model(&models.PairingCode{}).Where("id = ?", codeID).Update("is_used", true).Error; err != nil {
			return err
		}
		// Create the user
		if err := tx.Create(&user).Error; err != nil {
			return err
		}
		return ensurePerson(tx, user, channelName, role, false)
	})
}

// SaveWorkflow creates a new workflow macro.
func (c *Client) SaveWorkflow(wf models.Workflow) error {
	result := c.DB.Create(&wf)
	if result.Error != nil {
		return fmt.Errorf("failed to insert workflow: %w", result.Error)
	}
	return nil
}

// GetActiveWorkflows fetches all active workflows.
func (c *Client) GetActiveWorkflows() ([]models.Workflow, error) {
	var workflows []models.Workflow
	result := c.DB.Where("is_active = ?", true).Find(&workflows)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to fetch active workflows: %w", result.Error)
	}
	return workflows, nil
}

// GetUserWorkflows fetches all active workflows for a specific user.
func (c *Client) GetUserWorkflows(userID string) ([]models.Workflow, error) {
	var workflows []models.Workflow
	where, args := personIs("user_id", "person_id", userID)
	result := c.DB.Where(where, args...).Where("is_active = ?", true).Find(&workflows)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to fetch user workflows: %w", result.Error)
	}
	return workflows, nil
}

// DeleteWorkflow deactivates or deletes a workflow.
func (c *Client) DeleteWorkflow(id uint, userID string) error {
	where, args := personIs("user_id", "person_id", userID)
	result := c.DB.Where("id = ?", id).Where(where, args...).Delete(&models.Workflow{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete workflow: %w", result.Error)
	}
	return nil
}
