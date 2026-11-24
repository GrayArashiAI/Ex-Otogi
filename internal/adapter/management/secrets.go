package management

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// Secrets is the secrets settings refer to as ${NAME}, as the API sees
// them; the composition root implements it. No method returns a value.
type Secrets interface {
	// Secrets lists, by name, the secrets set in the panel and every name
	// settings refer to.
	Secrets(ctx context.Context) ([]Secret, error)
	// SetSecret sets a secret or replaces it, rebuilds the running modules
	// that refer to it, and returns those it restarted. Nothing is stored if
	// one does not build.
	SetSecret(ctx context.Context, name, value, by string) (restarted []string, err error)
	// DeleteSecret deletes a secret set in the panel and returns the modules
	// it restarted, which now read the environment variable of the name. The
	// error wraps ErrNotFound for a name not set, and ErrInUse when settings
	// refer to it and the environment has no such variable.
	DeleteSecret(ctx context.Context, name, by string) (restarted []string, err error)
}

// ErrInUse is what Secrets returns for deleting a secret settings need.
var ErrInUse = errors.New("in use")

// Secret is a secret's name and where its value is, never the value.
type Secret struct {
	Name       string    `json:"name"`
	Source     string    `json:"source" enum:"panel,environment,missing" doc:"Where its value is: set in the panel, which wins; an environment variable of the name; or missing, when settings refer to a name set in neither."`
	By         string    `json:"by,omitempty" doc:"Who set it in the panel."`
	At         time.Time `json:"at,omitzero" doc:"When it was set in the panel."`
	References []string  `json:"references" nullable:"false" doc:"The modules whose current settings refer to it."`
}

// SecretPath names a secret in a route.
type SecretPath struct {
	Name string `path:"name" pattern:"^[A-Za-z_][A-Za-z0-9_]*$" patternDescription:"letters, digits, and _, not starting with a digit" doc:"The secret's name, as settings refer to it: ${NAME}."`
}

// SecretValue is the body that sets a secret.
type SecretValue struct {
	Value string `json:"value" minLength:"1" doc:"The secret itself."`
}

type secretChanged struct {
	Body struct {
		Restarted []string `json:"restarted" nullable:"false" doc:"The modules rebuilt with the change."`
	}
}

// putSecret is the one operation whose request carries a secret.
const putSecret = "put-secret"

// withoutSecretValues keeps the values of a refused put-secret request out
// of the answer: huma's validation errors carry them, down to the whole
// body.
func withoutSecretValues(ctx huma.Context, _ string, v any) (any, error) {
	if model, ok := v.(*huma.ErrorModel); ok && ctx.Operation().OperationID == putSecret {
		for _, detail := range model.Errors {
			detail.Value = nil
		}
	}
	return v, nil
}

func registerSecrets(api huma.API, secrets Secrets) {
	changed := func(restarted []string, err error) (*secretChanged, error) {
		if err != nil {
			return nil, problem(err)
		}
		out := &secretChanged{}
		out.Body.Restarted = append([]string{}, restarted...)
		return out, nil
	}
	huma.Register(api, huma.Operation{OperationID: "list-secrets", Method: http.MethodGet, Path: "/api/secrets", Summary: "The secrets settings refer to, without their values",
		Description: "The secrets set in the panel, and every other name settings refer to as ${NAME}: one an environment variable provides, or one set nowhere.", Tags: []string{"Secrets"}},
		func(ctx context.Context, _ *struct{}) (*struct{ Body []Secret }, error) {
			all, err := secrets.Secrets(ctx)
			if err != nil {
				return nil, problem(err)
			}
			return &struct{ Body []Secret }{append([]Secret{}, all...)}, nil
		})
	huma.Register(api, huma.Operation{OperationID: putSecret, Method: http.MethodPut, Path: "/api/secrets/{name}", Summary: "Set or replace a secret",
		Description: "The running modules whose settings refer to the secret are rebuilt with the new value, which is stored only if they all build. It wins over an environment variable of the same name. Recorded as secret.changed, without the value.", Tags: []string{"Secrets"}},
		func(ctx context.Context, in *struct {
			SecretPath
			Body SecretValue
		},
		) (*secretChanged, error) {
			return changed(secrets.SetSecret(ctx, in.Name, in.Body.Value, caller(ctx)))
		})
	huma.Register(api, huma.Operation{OperationID: "delete-secret", Method: http.MethodDelete, Path: "/api/secrets/{name}", Summary: "Delete a secret set in the panel",
		Description: "Refused with 409, naming the settings, while settings refer to the secret and no environment variable of the name exists; with one, the modules referring to it are rebuilt to read that. Recorded as secret.changed.", Tags: []string{"Secrets"}},
		func(ctx context.Context, in *SecretPath) (*secretChanged, error) {
			return changed(secrets.DeleteSecret(ctx, in.Name, caller(ctx)))
		})
}
