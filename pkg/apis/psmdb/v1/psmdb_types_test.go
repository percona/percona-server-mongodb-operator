package v1

import (
	"testing"

	"gopkg.in/yaml.v2"
)

func TestSetValuesIfNotSet(t *testing.T) {
	tests := []struct {
		name     string
		cfg      MongoConfiguration
		input    map[string]interface{}
		expected MongoConfiguration
	}{
		{
			name: "Empty config",
			cfg:  MongoConfiguration(""),
			input: map[string]interface{}{
				"security": map[string]any{
					"authorization": "enabled",
					"ldap": map[string]any{
						"authz": map[string]any{
							"queryTemplate": "some-query",
						},
						"servers":           "server",
						"transportSecurity": "none",
						"bind": map[string]any{
							"queryUser":     "some-user",
							"queryPassword": "some-password",
						},
						"userToDNMapping": "some-mapping",
					},
				},
				"setParameter": map[string]any{
					"authenticationMechanisms": "PLAIN,SCRAM-SHA-1",
				},
			},
			expected: MongoConfiguration(`security:
  authorization: enabled
  ldap:
    authz:
      queryTemplate: some-query
    bind:
      queryPassword: some-password
      queryUser: some-user
    servers: server
    transportSecurity: none
    userToDNMapping: some-mapping
setParameter:
  authenticationMechanisms: PLAIN,SCRAM-SHA-1`),
		},
		{
			name: "Missing and different values",
			cfg: MongoConfiguration(`security:
  authorization: disabled
  ldap:
    authz:
      queryTemplate: query
    servers: other-server
    userToDNMapping: some-mapping`),
			input: map[string]interface{}{
				"security": map[string]any{
					"authorization": "enabled",
					"ldap": map[string]any{
						"authz": map[string]any{
							"queryTemplate": "some-query",
						},
						"servers":           "server",
						"transportSecurity": "none",
						"bind": map[string]any{
							"queryUser":     "some-user",
							"queryPassword": "some-password",
						},
						"userToDNMapping": "mapping",
					},
				},
				"setParameter": map[string]any{
					"authenticationMechanisms": "PLAIN,SCRAM-SHA-1",
				},
			},
			expected: MongoConfiguration(`security:
  authorization: disabled
  ldap:
    authz:
      queryTemplate: query
    bind:
      queryPassword: some-password
      queryUser: some-user
    servers: other-server
    transportSecurity: none
    userToDNMapping: some-mapping
setParameter:
  authenticationMechanisms: PLAIN,SCRAM-SHA-1`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cfg.SetValuesIfNotSet(tt.input); err != nil {
				t.Fatal(err)
			}

			remarshal := func(conf MongoConfiguration) MongoConfiguration {
				m := map[string]interface{}{}
				if err := yaml.Unmarshal([]byte(conf), m); err != nil {
					t.Fatal(err)
				}
				data, err := yaml.Marshal(m)
				if err != nil {
					t.Fatal(err)
				}
				return MongoConfiguration(data)
			}

			got := remarshal(tt.cfg)
			expected := remarshal(tt.expected)

			if got != expected {
				t.Fatalf("Expected:\n%s\nGot:\n%s", expected, got)
			}
		})
	}
}
