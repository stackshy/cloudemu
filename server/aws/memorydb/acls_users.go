package memorydb

import (
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/memorydb"

	"github.com/stackshy/cloudemu/v2/server/wire"
	mdbdriver "github.com/stackshy/cloudemu/v2/services/memorydb/driver"
)

func (h *Handler) createACL(w http.ResponseWriter, r *http.Request) {
	var in memorydb.CreateACLInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	acl, err := h.db.CreateACL(r.Context(), aws.ToString(in.ACLName), in.UserNames, tagMap(in.Tags))
	if err != nil {
		writeErr(w, "ACL", err)
		return
	}

	wire.WriteJSON(w, memorydb.CreateACLOutput{ACL: toWireACL(acl)})
}

//nolint:dupl // per-operation handlers share the decode-call-encode shape.
func (h *Handler) describeACLs(w http.ResponseWriter, r *http.Request) {
	var in memorydb.DescribeACLsInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	var names []string
	if in.ACLName != nil {
		names = []string{aws.ToString(in.ACLName)}
	}

	acls, err := h.db.DescribeACLs(r.Context(), names)
	if err != nil {
		writeErr(w, "ACL", err)
		return
	}

	page, next, err := paginate(acls, in.MaxResults, in.NextToken)
	if err != nil {
		writeErr(w, "ACL", err)
		return
	}

	out := memorydb.DescribeACLsOutput{NextToken: next}
	for i := range page {
		out.ACLs = append(out.ACLs, *toWireACL(&page[i]))
	}

	wire.WriteJSON(w, out)
}

func (h *Handler) updateACL(w http.ResponseWriter, r *http.Request) {
	var in memorydb.UpdateACLInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	acl, err := h.db.UpdateACL(r.Context(), aws.ToString(in.ACLName), in.UserNamesToAdd, in.UserNamesToRemove)
	if err != nil {
		writeErr(w, "ACL", err)
		return
	}

	wire.WriteJSON(w, memorydb.UpdateACLOutput{ACL: toWireACL(acl)})
}

func (h *Handler) deleteACL(w http.ResponseWriter, r *http.Request) {
	var in memorydb.DeleteACLInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	acl, err := h.db.DeleteACL(r.Context(), aws.ToString(in.ACLName))
	if err != nil {
		writeErr(w, "ACL", err)
		return
	}

	wire.WriteJSON(w, memorydb.DeleteACLOutput{ACL: toWireACL(acl)})
}

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	var in memorydb.CreateUserInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	cfg := mdbdriver.CreateUserConfig{
		Name: aws.ToString(in.UserName), AccessString: aws.ToString(in.AccessString), Tags: tagMap(in.Tags),
	}
	if in.AuthenticationMode != nil {
		cfg.AuthenticationType = string(in.AuthenticationMode.Type)
		cfg.Passwords = in.AuthenticationMode.Passwords
	}

	u, err := h.db.CreateUser(r.Context(), cfg)
	if err != nil {
		writeErr(w, "User", err)
		return
	}

	wire.WriteJSON(w, memorydb.CreateUserOutput{User: toWireUser(u)})
}

//nolint:dupl // per-operation handlers share the decode-call-encode shape.
func (h *Handler) describeUsers(w http.ResponseWriter, r *http.Request) {
	var in memorydb.DescribeUsersInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	var names []string
	if in.UserName != nil {
		names = []string{aws.ToString(in.UserName)}
	}

	users, err := h.db.DescribeUsers(r.Context(), names)
	if err != nil {
		writeErr(w, "User", err)
		return
	}

	page, next, err := paginate(users, in.MaxResults, in.NextToken)
	if err != nil {
		writeErr(w, "User", err)
		return
	}

	out := memorydb.DescribeUsersOutput{NextToken: next}
	for i := range page {
		out.Users = append(out.Users, *toWireUser(&page[i]))
	}

	wire.WriteJSON(w, out)
}

func (h *Handler) updateUser(w http.ResponseWriter, r *http.Request) {
	var in memorydb.UpdateUserInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	cfg := mdbdriver.UpdateUserConfig{Name: aws.ToString(in.UserName), AccessString: aws.ToString(in.AccessString)}
	if in.AuthenticationMode != nil {
		cfg.AuthenticationType = string(in.AuthenticationMode.Type)
		cfg.Passwords = in.AuthenticationMode.Passwords
	}

	u, err := h.db.UpdateUser(r.Context(), cfg)
	if err != nil {
		writeErr(w, "User", err)
		return
	}

	wire.WriteJSON(w, memorydb.UpdateUserOutput{User: toWireUser(u)})
}

func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	var in memorydb.DeleteUserInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	u, err := h.db.DeleteUser(r.Context(), aws.ToString(in.UserName))
	if err != nil {
		writeErr(w, "User", err)
		return
	}

	wire.WriteJSON(w, memorydb.DeleteUserOutput{User: toWireUser(u)})
}
