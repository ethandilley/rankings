package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"github.com/ethandilley/rankings/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9_-]+$`)

func generatePassword() (string, error) {
	buf := make([]byte, 9)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func main() {
	usernameFlag := flag.String("username", "", "")
	displayNameFlag := flag.String("display-name", "", "")
	passwordFlag := flag.String("password", "", "")
	adminFlag := flag.Bool("admin", false, "")
	flag.Parse()

	username := strings.ToLower(strings.TrimSpace(*usernameFlag))
	displayName := strings.TrimSpace(*displayNameFlag)
	if !usernamePattern.MatchString(username) {
		log.Fatal("username must contain only lowercase letters, numbers, underscores, or hyphens")
	}
	if displayName == "" {
		log.Fatal("display-name is required")
	}

	pass := *passwordFlag
	generated := false
	if pass == "" {
		var err error
		pass, err = generatePassword()
		if err != nil {
			log.Fatalf("generate password: %v", err)
		}
		generated = true
	}

	ctx := context.Background()
	dsn := os.Getenv("DB_URL")
	if dsn == "" {
		log.Fatal("DB_URL is required")
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	hash, err := bcrypt.GenerateFromPassword([]byte(pass), 12)
	if err != nil {
		log.Fatalf("hash password: %v", err)
	}

	q := db.New(conn)
	user, err := q.CreateUser(ctx, db.CreateUserParams{
		Username:     username,
		DisplayName:  displayName,
		PasswordHash: string(hash),
		IsAdmin:      *adminFlag,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			log.Fatalf("username %q already exists", username)
		}
		log.Fatalf("create user: %v", err)
	}

	fmt.Printf("created user %s (%s)\n", user.Username, user.DisplayName)
	if *adminFlag {
		fmt.Println("is_admin: true")
	}
	if generated {
		fmt.Printf("temporary password: %s\n", pass)
	}
}