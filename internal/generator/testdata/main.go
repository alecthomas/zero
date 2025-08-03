package main

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"

	"github.com/alecthomas/kong"
	"github.com/alecthomas/zero/providers/pubsub"
	zerosql "github.com/alecthomas/zero/providers/sql"
)

type DAL struct {
	users map[int]User
}

//zero:provider multi
func ProvideMigrations() zerosql.Migrations { return nil }

type CronLogger struct{}

//zero:provider weak
func ProvideCronLogger() CronLogger { return CronLogger{} }

type CronService struct{}

//zero:provider weak require=ProvideCronLogger
func ProvideCronService() CronService { return CronService{} }

//zero:provider
func NewDAL(db *sql.DB) (*DAL, error) {
	return &DAL{
		users: map[int]User{
			1: {Name: "Alice", BirthYear: 1945},
			2: {Name: "Bob", BirthYear: 1944},
		},
	}, nil
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
	Bind string `help:"The address to bind the service to"`
}

type Service struct {
	dal    *DAL
	config map[string]int
	tags   []string
}

//zero:provider
func NewService(dal *DAL, cronService CronService, config ServiceConfig, configMap map[string]int, tags []string) (*Service, error) {
	// Other initialisation
	return &Service{dal: dal, config: configMap, tags: tags}, nil
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

//zero:api GET /config
func (s *Service) GetConfig() map[string]int {
	return s.config
}

//zero:api GET /tags
func (s *Service) GetTags() []string {
	return s.tags
}

//zero:api POST /users authenticated role=admin
func (s *Service) CreateUser(user User) error {
	return s.dal.CreateUser(user)
}

//zero:api GET /users/{id}
func (s *Service) GetUser(id string) (User, error) {
	panic("not implemented")
}

//zero:api GET /users/{id}/avatar
func (s *Service) GetAvatar(id string, w http.ResponseWriter, r *http.Request) {

}

//zero:provider multi
func ProvideMapA() map[string]int {
	return map[string]int{
		"a": 1,
		"b": 2,
	}
}

//zero:provider multi
func ProvideMapB() map[string]int {
	return map[string]int{
		"c": 3,
		"d": 4,
	}
}

//zero:provider multi
func ProvideSliceA() []string {
	return []string{"apple", "banana"}
}

//zero:provider multi
func ProvideSliceB() []string {
	return []string{"cherry", "date"}
}

// zero:subscribe
func (s *Service) OnUserCreated(user pubsub.Event[UserCreatedEvent]) error {
	panic("not implemented")
}

//zero:cron 1h
func (s *Service) CheckUsers(ctx context.Context) error {
	panic("not implemented")
}

var cli struct {
	ZeroConfig
}

func main() {
	kctx := kong.Parse(&cli, kong.Vars{"sqldsn": "sqlite://:memory:"})
	ctx := context.Background()
	singletons := map[reflect.Type]any{}
	_, err := ZeroConstructSingletons[*Service](ctx, cli.ZeroConfig, singletons)
	kctx.FatalIfErrorf(err)
	mux, err := ZeroConstructSingletons[*http.ServeMux](ctx, cli.ZeroConfig, singletons)
	kctx.FatalIfErrorf(err)
	fmt.Println("Listening on http://127.0.0.1:8080")
	http.ListenAndServe("127.0.0.1:8080", mux)
}
