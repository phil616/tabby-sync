package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"tabby-config-sync/internal/config"
	"tabby-config-sync/internal/database"
	"tabby-config-sync/internal/httpapi"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	command := "serve"
	if len(args) > 0 {
		command = args[0]
		args = args[1:]
	}

	switch command {
	case "serve":
		return serve(args)
	case "user":
		return userCommand(args)
	case "healthcheck":
		return healthcheck(args)
	case "version":
		fmt.Printf("tabby-config-sync %s (commit=%s, built=%s)\n", version, commit, buildDate)
		return nil
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		printUsage()
		return fmt.Errorf("unknown command %q", command)
	}
}

func serve(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	level := new(slog.LevelVar)
	level.Set(cfg.LogLevel)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := database.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer db.Close()

	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           httpapi.New(db, logger, cfg.MaxBodyBytes, version),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    32 << 10,
	}

	signals, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	serverError := make(chan error, 1)
	go func() {
		logger.Info("server starting",
			"address", cfg.ListenAddress,
			"database", cfg.DatabasePath,
			"version", version,
		)
		serverError <- server.ListenAndServe()
	}()

	select {
	case <-signals.Done():
		logger.Info("shutdown signal received")
	case err := <-serverError:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if err := <-serverError; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", err)
	}
	logger.Info("server stopped")
	return nil
}

func userCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: tabby-config-sync user <create|rotate-token|list|enable|disable>")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := database.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer db.Close()

	switch args[0] {
	case "create":
		flags := flag.NewFlagSet("user create", flag.ContinueOnError)
		name := flags.String("name", "", "unique user name")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		user, token, err := db.CreateUser(ctx, *name)
		if err != nil {
			return err
		}
		fmt.Printf("User created: %s (id=%d)\n", user.Name, user.ID)
		fmt.Printf("Token: %s\n", token)
		fmt.Println("Store this token now. It cannot be recovered.")
		return nil
	case "rotate-token":
		flags := flag.NewFlagSet("user rotate-token", flag.ContinueOnError)
		name := flags.String("name", "", "user name")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		token, err := db.RotateUserToken(ctx, *name)
		if err != nil {
			return err
		}
		fmt.Printf("Token rotated for %s.\nToken: %s\n", strings.TrimSpace(*name), token)
		fmt.Println("Store this token now. It cannot be recovered.")
		return nil
	case "list":
		users, err := db.ListUsers(ctx)
		if err != nil {
			return err
		}
		writer := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "ID\tNAME\tENABLED\tCREATED")
		for _, user := range users {
			fmt.Fprintf(writer, "%d\t%s\t%t\t%s\n",
				user.ID,
				user.Name,
				user.Enabled,
				user.CreatedAt.Format(time.RFC3339),
			)
		}
		return writer.Flush()
	case "enable", "disable":
		flags := flag.NewFlagSet("user "+args[0], flag.ContinueOnError)
		name := flags.String("name", "", "user name")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		enabled := args[0] == "enable"
		if err := db.SetUserEnabled(ctx, *name, enabled); err != nil {
			return err
		}
		fmt.Printf("User %s: %s\n", strings.TrimSpace(*name), args[0]+"d")
		return nil
	default:
		return fmt.Errorf("unknown user command %q", args[0])
	}
}

func healthcheck(args []string) error {
	flags := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	url := flags.String("url", "http://127.0.0.1:8080/healthz", "health endpoint URL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(*url)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned %s", response.Status)
	}
	return nil
}

func printUsage() {
	fmt.Print(`Usage:
  tabby-config-sync serve
  tabby-config-sync user create --name <name>
  tabby-config-sync user rotate-token --name <name>
  tabby-config-sync user list
  tabby-config-sync user enable --name <name>
  tabby-config-sync user disable --name <name>
  tabby-config-sync healthcheck [--url <url>]
  tabby-config-sync version
`)
}
