// Package customer implements the Anchor Platform customer callback (SEP-12)
// with KYC through Dojah.
package customer

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"time"

	"aidmedium/stellar-anchor/internal/audit"
	"aidmedium/stellar-anchor/internal/kyc"

	"github.com/google/uuid"
)

const (
	StatusNeedsInfo  = "NEEDS_INFO"
	StatusProcessing = "PROCESSING"
	StatusAccepted   = "ACCEPTED"
	StatusRejected   = "REJECTED"
)

type FieldSpec struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Choices     []string `json:"choices,omitempty"`
	Optional    bool     `json:"optional,omitempty"`
}

type ProvidedField struct {
	FieldSpec
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type field struct {
	name string
	spec FieldSpec
}

var fieldList = []field{
	{"first_name", FieldSpec{Type: "string", Description: "First name, as on your BVN or NIN record"}},
	{"last_name", FieldSpec{Type: "string", Description: "Last name, as on your BVN or NIN record"}},
	{"email_address", FieldSpec{Type: "string", Description: "Email address"}},
	{"birth_date", FieldSpec{Type: "date", Description: "Date of birth (YYYY-MM-DD)"}},
	{"id_type", FieldSpec{Type: "string", Description: "Identity number type", Choices: []string{kyc.IDTypeBVN, kyc.IDTypeNIN}}},
	{"id_number", FieldSpec{Type: "string", Description: "BVN or NIN (11 digits)"}},
	{"bank_account_number", FieldSpec{Type: "string", Description: "Nigerian bank account number (10 digits)"}},
	{"bank_number", FieldSpec{Type: "string", Description: "Bank code of the account"}},
	{"mobile_number", FieldSpec{Type: "string", Description: "Mobile number", Optional: true}},
}

// identityFields are the fields Dojah checks. Changing any of them means KYC runs again.
var identityFields = []string{"first_name", "last_name", "birth_date", "id_type", "id_number"}

var (
	elevenDigits = regexp.MustCompile(`^[0-9]{11}$`)
	tenDigits    = regexp.MustCompile(`^[0-9]{10}$`)
	bankCode     = regexp.MustCompile(`^[0-9]{3,6}$`)
	phone        = regexp.MustCompile(`^\+?[0-9]{10,15}$`)
)

// ValidationError is returned for bad input and maps to HTTP 400.
type ValidationError struct{ msg string }

func (e *ValidationError) Error() string { return e.msg }

func invalid(format string, args ...any) error {
	return &ValidationError{fmt.Sprintf(format, args...)}
}

type Service struct {
	store Store
	kyc   kyc.Verifier
	audit audit.Logger
	now   func() time.Time
}

func NewService(store Store, verifier kyc.Verifier, auditLog audit.Logger) *Service {
	return &Service{store: store, kyc: verifier, audit: auditLog, now: time.Now}
}

type Query struct {
	ID      string
	Account string
	Memo    string
}

type Response struct {
	ID             string                   `json:"id,omitempty"`
	Status         string                   `json:"status"`
	Fields         map[string]FieldSpec     `json:"fields,omitempty"`
	ProvidedFields map[string]ProvidedField `json:"provided_fields,omitempty"`
	Message        string                   `json:"message,omitempty"`
}

func (s *Service) Get(ctx context.Context, q Query) (*Response, error) {
	c, err := s.find(ctx, q)
	if errors.Is(err, ErrNotFound) && q.ID == "" {
		return &Response{Status: StatusNeedsInfo, Fields: missingFields(nil)}, nil
	}
	if err != nil {
		return nil, err
	}
	if c.KYCPending {
		s.settle(ctx, c)
		if err := s.store.Save(ctx, c); err != nil {
			return nil, err
		}
	}
	return describe(c), nil
}

// Lookup returns the stored customer for a Stellar account, for payouts.
func (s *Service) Lookup(ctx context.Context, account, memo string) (*Customer, error) {
	return s.store.FindByAccount(ctx, account, memo)
}

type PutRequest struct {
	ID      string
	Account string
	Memo    string
	Fields  map[string]string
}

func (s *Service) Put(ctx context.Context, req PutRequest) (*Response, error) {
	if err := validate(req.Fields); err != nil {
		return nil, err
	}

	c, err := s.find(ctx, Query{ID: req.ID, Account: req.Account, Memo: req.Memo})
	switch {
	case errors.Is(err, ErrNotFound) && req.ID != "":
		return nil, ErrNotFound
	case errors.Is(err, ErrNotFound):
		if req.Account == "" {
			return nil, invalid("account is required")
		}
		now := s.now().UTC()
		c = &Customer{
			ID:        uuid.NewString(),
			Account:   req.Account,
			Memo:      req.Memo,
			Fields:    map[string]string{},
			CreatedAt: now,
		}
		s.audit.Record(ctx, audit.Entry{Action: "customer.created", CustomerID: c.ID})
	case err != nil:
		return nil, err
	}

	for _, f := range fieldList {
		if v, ok := req.Fields[f.name]; ok && v != "" {
			c.Fields[f.name] = v
		}
	}

	s.settle(ctx, c)

	c.UpdatedAt = s.now().UTC()
	if err := s.store.Save(ctx, c); err != nil {
		return nil, err
	}
	s.audit.Record(ctx, audit.Entry{Action: "customer.updated", CustomerID: c.ID, Detail: map[string]string{"status": c.Status}})
	return &Response{ID: c.ID, Status: c.Status}, nil
}

func (s *Service) Delete(ctx context.Context, id string) error {
	if err := s.store.Delete(ctx, id); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Entry{Action: "customer.deleted", CustomerID: id})
	return nil
}

func (s *Service) find(ctx context.Context, q Query) (*Customer, error) {
	if q.ID != "" {
		c, err := s.store.Get(ctx, q.ID)
		if err != nil {
			return nil, err
		}
		if q.Account != "" && (c.Account != q.Account || c.Memo != q.Memo) {
			return nil, ErrNotFound
		}
		return c, nil
	}
	if q.Account == "" {
		return nil, ErrNotFound
	}
	return s.store.FindByAccount(ctx, q.Account, q.Memo)
}

func (s *Service) verify(ctx context.Context, c *Customer) {
	dec, err := s.kyc.Verify(ctx, kyc.Identity{
		FirstName: c.Fields["first_name"],
		LastName:  c.Fields["last_name"],
		BirthDate: c.Fields["birth_date"],
		IDType:    c.Fields["id_type"],
		IDNumber:  c.Fields["id_number"],
	})
	if err != nil {
		c.Status, c.Message, c.KYCPending = StatusProcessing, "identity verification is in progress", true
		s.audit.Record(ctx, audit.Entry{Action: "kyc.deferred", CustomerID: c.ID, Detail: map[string]string{"error": err.Error()}})
		return
	}

	c.KYCPending = false
	c.Verified = map[string]string{}
	for _, name := range identityFields {
		c.Verified[name] = c.Fields[name]
	}
	switch dec.Status {
	case kyc.StatusAccepted:
		c.Status, c.Message = StatusAccepted, ""
	case kyc.StatusRejected:
		c.Status, c.Message = StatusRejected, dec.Reason
	default:
		c.Status, c.Message = StatusProcessing, dec.Reason
	}
	s.audit.Record(ctx, audit.Entry{Action: "kyc.decision", CustomerID: c.ID, Detail: map[string]string{"status": c.Status, "reason": dec.Reason}})
}

// settle runs KYC as soon as the identity fields are complete, so identity
// can be verified before the bank details are known. The customer is only
// ACCEPTED once every required field is present.
func (s *Service) settle(ctx context.Context, c *Customer) {
	switch {
	case !hasAll(c.Fields, identityFields):
		c.Status, c.Message, c.KYCPending = StatusNeedsInfo, "", false
	case identityChanged(c) || c.KYCPending:
		s.verify(ctx, c)
	case c.Status == StatusNeedsInfo:
		// Identity was accepted earlier and only other fields were missing.
		c.Status = StatusAccepted
	}
	if c.Status == StatusAccepted && len(missingFields(c.Fields)) > 0 {
		c.Status = StatusNeedsInfo
	}
}

// IdentityVerified reports whether KYC accepted the identity fields as they are now.
func (c *Customer) IdentityVerified() bool {
	if c == nil || c.KYCPending || identityChanged(c) {
		return false
	}
	return c.Status == StatusAccepted || c.Status == StatusNeedsInfo
}

func hasAll(have map[string]string, names []string) bool {
	for _, n := range names {
		if have[n] == "" {
			return false
		}
	}
	return true
}

func identityChanged(c *Customer) bool {
	if c.Verified == nil {
		return true
	}
	for _, name := range identityFields {
		if c.Verified[name] != c.Fields[name] {
			return true
		}
	}
	return false
}

func missingFields(have map[string]string) map[string]FieldSpec {
	out := map[string]FieldSpec{}
	for _, f := range fieldList {
		if !f.spec.Optional && have[f.name] == "" {
			out[f.name] = f.spec
		}
	}
	return out
}

func describe(c *Customer) *Response {
	resp := &Response{ID: c.ID, Status: c.Status, Message: c.Message, Fields: map[string]FieldSpec{}, ProvidedFields: map[string]ProvidedField{}}
	fieldStatus := StatusProcessing
	if c.Status == StatusAccepted || c.Status == StatusRejected {
		fieldStatus = c.Status
	}
	for _, f := range fieldList {
		if c.Fields[f.name] == "" {
			resp.Fields[f.name] = f.spec
			continue
		}
		resp.ProvidedFields[f.name] = ProvidedField{FieldSpec: f.spec, Status: fieldStatus}
	}
	return resp
}

func validate(fields map[string]string) error {
	if v := fields["email_address"]; v != "" {
		if a, err := mail.ParseAddress(v); err != nil || a.Address != v {
			return invalid("email_address is not a valid email address")
		}
	}
	if v := fields["birth_date"]; v != "" {
		d, err := time.Parse("2006-01-02", v)
		if err != nil {
			return invalid("birth_date must be YYYY-MM-DD")
		}
		if d.AddDate(18, 0, 0).After(time.Now()) {
			return invalid("customer must be at least 18 years old")
		}
	}
	if v := fields["id_type"]; v != "" && v != kyc.IDTypeBVN && v != kyc.IDTypeNIN {
		return invalid("id_type must be bvn or nin")
	}
	if v := fields["id_number"]; v != "" && !elevenDigits.MatchString(v) {
		return invalid("id_number must be 11 digits")
	}
	if v := fields["bank_account_number"]; v != "" && !tenDigits.MatchString(v) {
		return invalid("bank_account_number must be 10 digits")
	}
	if v := fields["bank_number"]; v != "" && !bankCode.MatchString(v) {
		return invalid("bank_number must be a bank code")
	}
	if v := fields["mobile_number"]; v != "" && !phone.MatchString(v) {
		return invalid("mobile_number is not a valid phone number")
	}
	for _, name := range []string{"first_name", "last_name"} {
		if len(fields[name]) > 100 {
			return invalid("%s is too long", name)
		}
	}
	return nil
}
