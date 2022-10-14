package mongo

import (
	"context"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
)

type UserManager interface {
	GetRole(ctx context.Context, role string) (*Role, error)
	CreateRole(ctx context.Context, role string, privileges []RolePrivilege, roles []interface{}) error
	UpdateRole(ctx context.Context, role string, privileges []RolePrivilege, roles []interface{}) error
	GetUserInfo(ctx context.Context, username string) (*User, error)
	CreateUser(ctx context.Context, user, pwd string, roles ...map[string]interface{}) error
	UpdateUser(ctx context.Context, currName, newName, pass string) error
	UpdateUserPass(ctx context.Context, name, pass string) error
	UpdateUserRoles(ctx context.Context, username string, roles []map[string]interface{}) error
}

func (c *client) CreateRole(ctx context.Context, role string, privileges []RolePrivilege, roles []interface{}) error {
	resp := OKResponse{}

	privilegesArr := bson.A{}
	for _, p := range privileges {
		privilegesArr = append(privilegesArr, p)
	}

	rolesArr := bson.A{}
	for _, r := range roles {
		rolesArr = append(rolesArr, r)
	}

	m := bson.D{
		{Key: "createRole", Value: role},
		{Key: "privileges", Value: privilegesArr},
		{Key: "roles", Value: rolesArr},
	}

	res := c.conn.Database("admin").RunCommand(ctx, m)
	if res.Err() != nil {
		return errors.Wrap(res.Err(), "failed to create role")
	}

	err := res.Decode(&resp)
	if err != nil {
		return errors.Wrap(err, "failed to decode response")
	}

	if resp.OK != 1 {
		return errors.Errorf("mongo says: %s", resp.Errmsg)
	}

	return nil
}

func (c *client) UpdateRole(ctx context.Context, role string, privileges []RolePrivilege, roles []interface{}) error {
	resp := OKResponse{}

	privilegesArr := bson.A{}
	for _, p := range privileges {
		privilegesArr = append(privilegesArr, p)
	}

	rolesArr := bson.A{}
	for _, r := range roles {
		rolesArr = append(rolesArr, r)
	}

	m := bson.D{
		{Key: "updateRole", Value: role},
		{Key: "privileges", Value: privilegesArr},
		{Key: "roles", Value: rolesArr},
	}

	res := c.conn.Database("admin").RunCommand(ctx, m)
	if res.Err() != nil {
		return errors.Wrap(res.Err(), "failed to create role")
	}

	err := res.Decode(&resp)
	if err != nil {
		return errors.Wrap(err, "failed to decode response")
	}

	if resp.OK != 1 {
		return errors.Errorf("mongo says: %s", resp.Errmsg)
	}

	return nil
}

func (c *client) GetRole(ctx context.Context, role string) (*Role, error) {
	resp := RoleInfo{}

	res := c.conn.Database("admin").RunCommand(ctx, bson.D{
		{Key: "rolesInfo", Value: role},
		{Key: "showPrivileges", Value: true},
	})
	if res.Err() != nil {
		return nil, errors.Wrap(res.Err(), "run command")
	}

	err := res.Decode(&resp)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode response")
	}
	if resp.OK != 1 {
		return nil, errors.Errorf("mongo says: %s", resp.Errmsg)
	}
	if len(resp.Roles) == 0 {
		return nil, nil
	}
	return &resp.Roles[0], nil
}

func (c *client) CreateUser(ctx context.Context, user, pwd string, roles ...map[string]interface{}) error {
	resp := OKResponse{}

	res := c.conn.Database("admin").RunCommand(ctx, bson.D{
		{Key: "createUser", Value: user},
		{Key: "pwd", Value: pwd},
		{Key: "roles", Value: roles},
	})
	if res.Err() != nil {
		return errors.Wrap(res.Err(), "failed to create user")
	}

	err := res.Decode(&resp)
	if err != nil {
		return errors.Wrap(err, "failed to decode response")
	}

	if resp.OK != 1 {
		return errors.Errorf("mongo says: %s", resp.Errmsg)
	}

	return nil
}

func (c *client) GetUserInfo(ctx context.Context, username string) (*User, error) {
	resp := UsersInfo{}
	res := c.conn.Database("admin").RunCommand(ctx, bson.D{{Key: "usersInfo", Value: username}})
	if res.Err() != nil {
		return nil, errors.Wrap(res.Err(), "run command")
	}

	err := res.Decode(&resp)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode response")
	}
	if resp.OK != 1 {
		return nil, errors.Errorf("mongo says: %s", resp.Errmsg)
	}
	if len(resp.Users) == 0 {
		return nil, nil
	}
	return &resp.Users[0], nil
}

func (c *client) UpdateUserRoles(ctx context.Context, username string, roles []map[string]interface{}) error {
	return c.conn.Database("admin").RunCommand(ctx, bson.D{{Key: "updateUser", Value: username}, {Key: "roles", Value: roles}}).Err()
}

func (c *client) UpdateUserPass(ctx context.Context, name, pass string) error {
	return c.conn.Database("admin").RunCommand(ctx, bson.D{{Key: "updateUser", Value: name}, {Key: "pwd", Value: pass}}).Err()
}

// UpdateUser recreates user with new name and password
// should be used only when username was changed
func (c *client) UpdateUser(ctx context.Context, currName, newName, pass string) error {
	mu := struct {
		Users []struct {
			Roles interface{} `bson:"roles"`
		} `bson:"users"`
	}{}

	res := c.conn.Database("admin").RunCommand(ctx, bson.D{{Key: "usersInfo", Value: currName}})
	if res.Err() != nil {
		return errors.Wrap(res.Err(), "get user")
	}
	err := res.Decode(&mu)
	if err != nil {
		return errors.Wrap(err, "decode user")
	}

	if len(mu.Users) == 0 {
		return errors.New("empty user data")
	}

	err = c.conn.Database("admin").RunCommand(ctx, bson.D{
		{Key: "createUser", Value: newName},
		{Key: "pwd", Value: pass},
		{Key: "roles", Value: mu.Users[0].Roles},
	}).Err()
	if err != nil {
		return errors.Wrap(err, "create user")
	}

	err = c.conn.Database("admin").RunCommand(ctx, bson.D{{Key: "dropUser", Value: currName}}).Err()
	return errors.Wrap(err, "drop user")
}
