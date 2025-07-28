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

	"github.com/alecthomas/kong"
	"github.com/alecthomas/zero"
	zerosql "github.com/alecthomas/zero/providers/sql"
)

type DAL struct {
	users map[int]User
}

//go:embed migrations
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
	nextId := slices.Max(slices.Collect(maps.Keys(d.users))) + 1
	d.users[nextId] = user
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
	Bind string ``
}

type Service struct {
	dal *DAL
}

//zero:provider
func NewService(dal *DAL, config ServiceConfig) (*Service, error) {
	// Other initialisation
	return &Service{dal: dal}, nil
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
func (s *Service) OnUserCreated(user zero.Event[UserCreatedEvent]) error {
	panic("not implemented")
}

// zero:cron 1h
func (s *Service) CheckUsers() error {
	panic("not implemented")
}

var cli struct {
	Dev bool `help:"Run in development mode."`
	ZeroConfig
}

func main() {
	kctx := kong.Parse(&cli)
	ctx := context.Background()
	singletons := map[reflect.Type]any{}
	logger, err := ZeroConstructSingletons[*slog.Logger](ctx, cli.ZeroConfig, singletons)
	kctx.FatalIfErrorf(err)
	mux, err := ZeroConstructSingletons[*http.ServeMux](ctx, cli.ZeroConfig, singletons)
	kctx.FatalIfErrorf(err)
	logger.Info("Listening on http://127.0.0.1:8080")
	http.ListenAndServe("127.0.0.1:8080", mux)
}
