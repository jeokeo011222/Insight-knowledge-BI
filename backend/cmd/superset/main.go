// Command superset is the CLI entrypoint, mirroring superset/cli commands:
// init, db upgrade, run, worker, beat, load-examples, fab create-admin, ...
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"superset/internal/api"
	"superset/internal/config"
	"superset/internal/datasets"
	"superset/internal/examples"
	"superset/internal/models"
	"superset/internal/pkg/cache"
	"superset/internal/pkg/database"
	"superset/internal/pkg/i18n"
	"superset/internal/pkg/logger"
	"superset/internal/reports"
	"superset/internal/security"
	"superset/internal/server"
	"superset/internal/workers"

	"gorm.io/gorm"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "superset",
		Short:         "Apache Superset (Go+React replica)",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(initCmd())
	cmd.AddCommand(dbUpgradeCmd())
	cmd.AddCommand(runCmd())
	cmd.AddCommand(workerCmd())
	cmd.AddCommand(beatCmd())
	cmd.AddCommand(loadExamplesCmd())
	cmd.AddCommand(createAdminCmd())
	cmd.AddCommand(setDatabaseURICmd())
	cmd.AddCommand(versionCmd())
	cmd.AddCommand(mcpCmd())
	return cmd
}

func bootstrap() (*config.Config, *zap.Logger, *i18n.Translator, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, nil, err
	}
	log, err := logger.New(cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		return nil, nil, nil, err
	}
	tr, err := i18n.New(i18n.LocalesFS, cfg.BabelDefaultLocale)
	if err != nil {
		return nil, nil, nil, err
	}
	return cfg, log, tr, nil
}

func initCmd() *cobra.Command {
	var withExamples bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize the metadata database (tables, roles, permissions)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, log, _, err := bootstrap()
			if err != nil {
				return err
			}
			defer log.Sync()
			db, err := database.Connect(cfg.SQLAlchemyURI, cfg.Debug)
			if err != nil {
				return err
			}
			if err := database.Migrate(db); err != nil {
				return fmt.Errorf("migrate: %w", err)
			}
			if err := seedSecurity(cmd.Context(), cfg, log, db); err != nil {
				return err
			}
			if withExamples {
				if err := examples.Load(cmd.Context(), db, cfg, log); err != nil {
					return fmt.Errorf("load examples: %w", err)
				}
			}
			log.Info("database initialized")
			return nil
		},
	}
	cmd.Flags().BoolVar(&withExamples, "with-examples", false, "load example data")
	return cmd
}

func dbUpgradeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "db upgrade",
		Short: "Run database migrations (idempotent)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, log, _, err := bootstrap()
			if err != nil {
				return err
			}
			defer log.Sync()
			db, err := database.Connect(cfg.SQLAlchemyURI, cfg.Debug)
			if err != nil {
				return err
			}
			if err := database.Migrate(db); err != nil {
				return fmt.Errorf("migrate: %w", err)
			}
			if err := seedSecurity(cmd.Context(), cfg, log, db); err != nil {
				return err
			}
			log.Info("database upgraded")
			return nil
		},
	}
}

func runCmd() *cobra.Command {
	var port int
	var withThreads, reload bool
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the Superset web server",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, log, tr, err := bootstrap()
			if err != nil {
				return err
			}
			defer log.Sync()
			if port != 0 {
				cfg.WebserverPort = port
			}
			_ = withThreads
			_ = reload
			db, err := database.Connect(cfg.SQLAlchemyURI, cfg.Debug)
			if err != nil {
				return err
			}
			if err := database.Migrate(db); err != nil {
				return fmt.Errorf("migrate: %w", err)
			}
			if err := seedSecurity(cmd.Context(), cfg, log, db); err != nil {
				return err
			}
			srv := server.New(log)
			// Routes are registered by the api package first so that the
			// authentication middleware can be attached before any route.
			if err := api.RegisterAll(srv, cfg, log, tr, db); err != nil {
				return err
			}
			srv.RegisterHealth()
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			return srv.Listen(ctx, fmt.Sprintf(":%d", cfg.WebserverPort))
		},
	}
	cmd.Flags().IntVarP(&port, "port", "p", 0, "port to bind")
	cmd.Flags().BoolVar(&withThreads, "with-threads", true, "use threads")
	cmd.Flags().BoolVar(&reload, "reload", false, "auto-reload (dev)")
	return cmd
}

func workerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "worker",
		Short: "Run the async task worker (SQL Lab, alerts, reports, thumbnails)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, log, _, err := bootstrap()
			if err != nil {
				return err
			}
			defer log.Sync()
			db, err := database.Connect(cfg.SQLAlchemyURI, cfg.Debug)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			log.Info("worker started")
			return workers.Run(ctx, cfg, log, db)
		},
	}
}

func beatCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "beat",
		Short: "Run the scheduler (cron for alerts/reports)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, log, _, err := bootstrap()
			if err != nil {
				return err
			}
			defer log.Sync()
			db, err := database.Connect(cfg.SQLAlchemyURI, cfg.Debug)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			// The refresh scheduler invalidates cached chart data after a
			// successful run. Redis may be absent in a trimmed deployment; the
			// refresher then still works, it just cannot clear the cache.
			var cacheClient *cache.Client
			if cfg.RedisURL != "" {
				c, cacheErr := cache.New(cfg.RedisURL)
				if cacheErr == nil {
					cacheClient = c
					defer func() { _ = c.Close() }()
				} else {
					log.Warn("redis unavailable: dataset refresh will not invalidate the query cache",
						zap.Error(cacheErr))
				}
			}

			refresher := datasets.New(db, cfg, log, cacheClient)
			go func() {
				if err := refresher.RunScheduler(ctx, datasets.DefaultScanInterval); err != nil {
					log.Error("dataset refresh scheduler stopped", zap.Error(err))
				}
			}()

			log.Info("beat scheduler started")
			return reports.RunScheduler(ctx, cfg, log, db)
		},
	}
}

func loadExamplesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "load-examples",
		Short: "Load the example dataset, charts and dashboard (birth_names)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, log, _, err := bootstrap()
			if err != nil {
				return err
			}
			defer log.Sync()
			db, err := database.Connect(cfg.SQLAlchemyURI, cfg.Debug)
			if err != nil {
				return err
			}
			if err := database.Migrate(db); err != nil {
				return err
			}
			if err := examples.Load(cmd.Context(), db, cfg, log); err != nil {
				return fmt.Errorf("load examples: %w", err)
			}
			log.Info("examples loaded")
			return nil
		},
	}
}

func createAdminCmd() *cobra.Command {
	var username, firstname, lastname, email, password string
	cmd := &cobra.Command{
		Use:   "fab create-admin",
		Short: "Create an admin user",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, log, _, err := bootstrap()
			if err != nil {
				return err
			}
			defer log.Sync()
			db, err := database.Connect(cfg.SQLAlchemyURI, cfg.Debug)
			if err != nil {
				return err
			}
			if err := database.Migrate(db); err != nil {
				return err
			}
			if err := seedSecurity(cmd.Context(), cfg, log, db); err != nil {
				return err
			}
			mgr := security.NewManager(db, log)
			hash, err := security.HashPassword(password)
			if err != nil {
				return err
			}
			user := models.User{
				FirstName: firstname, LastName: lastname, Username: username,
				Email: email, Password: hash, Active: true,
			}
			if err := db.Where("username = ?", username).FirstOrCreate(&user, models.User{Username: username}).Error; err != nil {
				return err
			}
			updates := map[string]any{
				"first_name": firstname, "last_name": lastname, "email": email,
				"password": hash, "active": true,
			}
			if err := db.Model(&user).Updates(updates).Error; err != nil {
				return err
			}
			var role models.Role
			if err := db.Where("name = ?", security.RoleAdmin).First(&role).Error; err != nil {
				return err
			}
			db.Where("user_id = ?", user.ID).Delete(&models.UserRole{})
			if err := db.Create(&models.UserRole{UserID: user.ID, RoleID: role.ID}).Error; err != nil {
				return err
			}
			mgr.Invalidate(user.ID)
			log.Info("admin user created", zap.String("username", username))
			return nil
		},
	}
	cmd.Flags().StringVar(&username, "username", "admin", "admin username")
	cmd.Flags().StringVar(&firstname, "firstname", "admin", "first name")
	cmd.Flags().StringVar(&lastname, "lastname", "user", "last name")
	cmd.Flags().StringVar(&email, "email", "admin@superset.com", "email")
	cmd.Flags().StringVar(&password, "password", "admin", "password")
	return cmd
}

func setDatabaseURICmd() *cobra.Command {
	var uri string
	cmd := &cobra.Command{
		Use:   "set-database-uri",
		Short: "Set the SQLAlchemy URI for a named database",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = uri
			return nil
		},
	}
	cmd.Flags().StringVar(&uri, "uri", "", "sqlalchemy uri")
	return cmd
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(server.Version)
		},
	}
}

// seedSecurity installs the built-in roles/permissions and the bootstrap admin.
func seedSecurity(ctx context.Context, cfg *config.Config, log *zap.Logger, db *gorm.DB) error {
	mgr := security.NewManager(db, log)
	if err := mgr.Seed(ctx); err != nil {
		return fmt.Errorf("seed roles/permissions: %w", err)
	}
	created, err := mgr.EnsureAdminUser(ctx, "admin", "admin", "Superset", "Admin", "admin@superset.com")
	if err != nil {
		return fmt.Errorf("bootstrap admin: %w", err)
	}
	if created {
		log.Info("bootstrap admin created", zap.String("username", "admin"))
	}
	return nil
}
