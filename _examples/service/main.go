package main

import (
	"context"
	"database/sql"
	"embed"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"time"

	"github.com/alecthomas/kong"
	kongtoml "github.com/alecthomas/kong-toml"
	"github.com/alecthomas/zero"
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

//zero:config prefix="server-"
type ServiceConfig struct {
	Bind string `help:"The address to bind the server to."`
}

type Service struct {
	dal    *DAL
	logger *slog.Logger
}

//zero:provider
func NewService(dal *DAL, logger *slog.Logger, topic pubsub.Topic[User], config ServiceConfig) (*Service, error) {
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

// zero:subscribe
func (s *Service) OnUserCreated(user pubsub.Event[UserCreatedEvent]) error {
	panic("not implemented")
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
	singletons := map[reflect.Type]any{}

	if cli.SQLDumpMigrations != "" {
		migrations, err := ZeroConstructSingletons[zerosql.Migrations](ctx, cli.ZeroConfig, singletons)
		kctx.FatalIfErrorf(err)
		err = zerosql.DumpMigrations(migrations, cli.SQLDumpMigrations)
		kctx.FatalIfErrorf(err)
		kctx.Exit(0)
	}

	logger, err := ZeroConstructSingletons[*slog.Logger](ctx, cli.ZeroConfig, singletons)
	kctx.FatalIfErrorf(err)
	container, err := ZeroConstructSingletons[*zero.Container](ctx, cli.ZeroConfig, singletons)
	kctx.FatalIfErrorf(err)
	logger.Info("Listening on http://127.0.0.1:8080")
	http.ListenAndServe("127.0.0.1:8080", container)
}
