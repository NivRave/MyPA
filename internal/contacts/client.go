package contacts

import (
	"context"
	"fmt"

	"github.com/nivik/mypa/internal/calendar"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/state"
	"google.golang.org/api/option"
	"google.golang.org/api/people/v1"
)

type Client struct {
	oauthConfig *calendar.OAuthConfig
	store       *state.Store
	endpoint    string // used for testing
}

// NewClient creates a new Contacts client.
func NewClient(oauthCfg *calendar.OAuthConfig, store *state.Store) *Client {
	return &Client{
		oauthConfig: oauthCfg,
		store:       store,
	}
}

func (c *Client) getService(ctx context.Context, userID string) (*people.Service, error) {
	tokenData, err := c.store.GetOAuthToken(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("no token found for user %s: %w", userID, err)
	}

	token, err := calendar.DecodeToken([]byte(tokenData))
	if err != nil {
		return nil, fmt.Errorf("invalid token format: %w", err)
	}

	client := c.oauthConfig.TokenSource(ctx, token)
	opts := []option.ClientOption{option.WithTokenSource(client)}
	if c.endpoint != "" {
		opts = append(opts, option.WithEndpoint(c.endpoint))
	}
	return people.NewService(ctx, opts...)
}

// SearchContacts searches the user's Google Contacts by name.
func (c *Client) SearchContacts(ctx context.Context, userID, query string) ([]models.Contact, error) {
	srv, err := c.getService(ctx, userID)
	if err != nil {
		return nil, err
	}

	req := srv.People.SearchContacts().Query(query).ReadMask("names,emailAddresses,phoneNumbers")
	resp, err := req.Do()
	if err != nil {
		return nil, fmt.Errorf("failed to search contacts: %w", err)
	}

	var results []models.Contact
	for _, person := range resp.Results {
		if person.Person == nil {
			continue
		}
		
		contact := models.Contact{
			ResourceName: person.Person.ResourceName,
		}

		if len(person.Person.Names) > 0 {
			contact.Name = person.Person.Names[0].DisplayName
		}
		if len(person.Person.EmailAddresses) > 0 {
			contact.Email = person.Person.EmailAddresses[0].Value
		}
		if len(person.Person.PhoneNumbers) > 0 {
			contact.Phone = person.Person.PhoneNumbers[0].Value
		}

		results = append(results, contact)
	}

	return results, nil
}

// CreateContact creates a new Google Contact.
func (c *Client) CreateContact(ctx context.Context, userID, name, email, phone string) error {
	srv, err := c.getService(ctx, userID)
	if err != nil {
		return err
	}

	person := &people.Person{}

	if name != "" {
		person.Names = []*people.Name{
			{GivenName: name},
		}
	}
	if email != "" {
		person.EmailAddresses = []*people.EmailAddress{
			{Value: email},
		}
	}
	if phone != "" {
		person.PhoneNumbers = []*people.PhoneNumber{
			{Value: phone},
		}
	}

	_, err = srv.People.CreateContact(person).Do()
	if err != nil {
		return fmt.Errorf("failed to create contact: %w", err)
	}

	return nil
}
