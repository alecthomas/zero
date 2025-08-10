package main

import (
	"context"
	"database/sql"
	"embed"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/alecthomas/kong"
	kongtoml "github.com/alecthomas/kong-toml"
	"github.com/alecthomas/zero/providers/pubsub"
	zerosql "github.com/alecthomas/zero/providers/sql"
)

type DAL struct {
	users map[int]User
}

//go:embed migrations/*.sql
var migrations embed.FS

//zero:provider multi
func Migrations() zerosql.Migrations {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		panic(err)
	}
	return zerosql.Migrations{sub}
}

//zero:provider
func NewDAL(db *sql.DB) *DAL {
	return &DAL{
		users: map[int]User{
			1: {Name: "Alice", BirthYear: 1970},
			2: {Name: "Bob", BirthYear: 1980},
		},
	}
}

func (d *DAL) GetUsers() ([]User, error) {
	return slices.Collect(maps.Values(d.users)), nil
}

func (d *DAL) CreateUser(user User) error {
	nextID := slices.Max(slices.Collect(maps.Keys(d.users))) + 1
	d.users[nextID] = user
	return nil
}

//zero:middleware authenticated role
func Authenticate(role string) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}

type Service struct {
	dal    *DAL
	logger *slog.Logger
}

//zero:provider
func NewService(dal *DAL, logger *slog.Logger, topic pubsub.Topic[User]) (*Service, error) {
	// Other initialisation
	return &Service{dal: dal, logger: logger}, nil
}

type User struct {
	Name      string `json:"name"`
	BirthYear int    `json:"birthYear"`
}

type UserCreatedEvent User

func (u UserCreatedEvent) ID() string { return u.Name }

//zero:api GET /users
func (s *Service) ListUsers() ([]User, error) {
	return s.dal.GetUsers()
}

//zero:api POST /users authenticated role=admin
func (s *Service) CreateUser(user User) error {
	return s.dal.CreateUser(user)
}

//zero:api GET /users/{id}
func (s *Service) GetUser(id string) (User, error) {
	panic("not implemented")
}

//zero:subscribe
func (s *Service) OnUserCreated(ctx context.Context, user pubsub.Event[UserCreatedEvent]) error {
	s.logger.Info("OnUserCreated received event", "user", user.Payload().Name)
	return nil
}

//zero:cron 5s
func (s *Service) CheckUsersCron(ctx context.Context) error {
	s.logger.Info("CheckUsers cron job")
	time.Sleep(time.Second * 7)
	return nil
}

var cli struct {
	Config            kong.ConfigFlag `help:"Path to the configuration file."`
	SQLDumpMigrations string          `help:"Dump SQL migrations." type:"existingdir"`
	ZeroConfig
}

func main() {
	kctx := kong.Parse(&cli,
		kong.Configuration(kongtoml.Loader, "zero-exemplar.toml"),
		kong.Vars{
			"sqldsn": "postgres://postgres:secret@localhost:5432/zero-exempler?sslmode=disable",
		},
	)
	ctx := context.Background()

	if cli.SQLDumpMigrations != "" {
		migrations, err := ZeroConstruct[zerosql.Migrations](ctx, cli.ZeroConfig)
		kctx.FatalIfErrorf(err)
		err = zerosql.DumpMigrations(migrations, cli.SQLDumpMigrations)
		kctx.FatalIfErrorf(err)
		kctx.Exit(0)
	}

	err := Run(ctx, cli.ZeroConfig)
	kctx.FatalIfErrorf(err)
}
