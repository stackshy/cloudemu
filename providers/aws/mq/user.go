package mq

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/services/mq/driver"
)

// CreateUser adds a broker user. Its password is stored (write-only) so a later
// update can preserve it, but is never echoed back on DescribeUser.
func (m *Mock) CreateUser(_ context.Context, brokerID string, user *driver.User) error {
	if user.Username == "" {
		return badRequest("username is required")
	}

	return m.mutateBrokerUsers(brokerID, func(users []driver.User) ([]driver.User, error) {
		if findUser(users, user.Username) >= 0 {
			return nil, conflict("Broker user %s already exists", user.Username)
		}

		u := *user
		u.Groups = append([]string(nil), user.Groups...)

		return append(users, u), nil
	})
}

// DescribeUser returns a broker user with its password blanked, mirroring the
// real API which never echoes a user's password.
func (m *Mock) DescribeUser(_ context.Context, brokerID, username string) (*driver.User, error) {
	b, ok := m.brokers.Get(brokerID)
	if !ok {
		return nil, notFound("Broker %s not found", brokerID)
	}

	i := findUser(b.Users, username)
	if i < 0 {
		return nil, notFound("Broker user %s not found", username)
	}

	u := b.Users[i]
	u.Password = ""

	u.Groups = append([]string(nil), b.Users[i].Groups...)

	return &u, nil
}

// UpdateUser replaces a broker user's attributes. An empty password preserves
// the stored one, so an update that changes only the groups keeps the password.
func (m *Mock) UpdateUser(_ context.Context, brokerID string, user *driver.User) error {
	return m.mutateBrokerUsers(brokerID, func(users []driver.User) ([]driver.User, error) {
		i := findUser(users, user.Username)
		if i < 0 {
			return nil, notFound("Broker user %s not found", user.Username)
		}

		existing := users[i]
		if user.Password != "" {
			existing.Password = user.Password
		}

		existing.ConsoleAccess = user.ConsoleAccess
		existing.ReplicationUser = user.ReplicationUser
		existing.Groups = append([]string(nil), user.Groups...)
		users[i] = existing

		return users, nil
	})
}

// DeleteUser removes a broker user.
func (m *Mock) DeleteUser(_ context.Context, brokerID, username string) error {
	return m.mutateBrokerUsers(brokerID, func(users []driver.User) ([]driver.User, error) {
		i := findUser(users, username)
		if i < 0 {
			return nil, notFound("Broker user %s not found", username)
		}

		return append(users[:i], users[i+1:]...), nil
	})
}

// ListUsers returns a deterministic page of broker users (usernames), sorted by
// username. Passwords are blanked.
func (m *Mock) ListUsers(_ context.Context, brokerID string, page driver.Page) ([]driver.User, string, error) {
	b, ok := m.brokers.Get(brokerID)
	if !ok {
		return nil, "", notFound("Broker %s not found", brokerID)
	}

	users := copyUsers(b.Users)
	for i := range users {
		users[i].Password = ""
	}

	sort.Slice(users, func(i, j int) bool { return users[i].Username < users[j].Username })

	start, end, next := paginate(len(users), page)

	return users[start:end], next, nil
}

// mutateBrokerUsers applies fn to a fresh copy of the broker's user list under
// the store lock, persisting the result only when fn succeeds.
func (m *Mock) mutateBrokerUsers(brokerID string, fn func([]driver.User) ([]driver.User, error)) error {
	var opErr error

	found := m.brokers.Update(brokerID, func(b driver.Broker) driver.Broker {
		newUsers, err := fn(copyUsers(b.Users))
		if err != nil {
			opErr = err

			return b
		}

		b.Users = newUsers

		return b
	})
	if !found {
		return notFound("Broker %s not found", brokerID)
	}

	return opErr
}

// findUser returns the index of the user with the given username, or -1.
func findUser(users []driver.User, username string) int {
	for i := range users {
		if users[i].Username == username {
			return i
		}
	}

	return -1
}
