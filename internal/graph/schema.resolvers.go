package graph

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"time"

	dbmodels "github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/graph/model"
)

// GeneratePairingCode is the resolver for the generatePairingCode field.
func (r *mutationResolver) GeneratePairingCode(ctx context.Context, role string, familyGroup string) (*model.PairingCode, error) {
	// Generate a 6-digit code
	n, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	code := fmt.Sprintf("%06d", n.Int64())

	pc := dbmodels.PairingCode{
		Code:        code,
		Role:        role,
		FamilyGroup: familyGroup,
		ExpiresAt:   time.Now().Add(24 * time.Hour),
		IsUsed:      false,
	}

	if err := r.DB.CreatePairingCode(pc); err != nil {
		return nil, err
	}

	return &model.PairingCode{
		ID:          pc.ID,
		Code:        pc.Code,
		Role:        pc.Role,
		FamilyGroup: pc.FamilyGroup,
		ExpiresAt:   pc.ExpiresAt,
		IsUsed:      pc.IsUsed,
		CreatedAt:   pc.CreatedAt,
	}, nil
}

// Users is the resolver for the users field.
func (r *queryResolver) Users(ctx context.Context) ([]*model.User, error) {
	var dbUsers []dbmodels.User
	if err := r.DB.DB.Find(&dbUsers).Error; err != nil {
		return nil, err
	}

	var users []*model.User
	for _, u := range dbUsers {
		users = append(users, &model.User{
			ID:          u.ID,
			PlatformID:  u.PlatformID,
			Name:        u.Name,
			Role:        u.Role,
			FamilyGroup: u.FamilyGroup,
			CreatedAt:   u.CreatedAt,
		})
	}
	return users, nil
}

// ActivePairingCodes is the resolver for the activePairingCodes field.
func (r *queryResolver) ActivePairingCodes(ctx context.Context) ([]*model.PairingCode, error) {
	var dbCodes []dbmodels.PairingCode
	if err := r.DB.DB.Where("is_used = ? AND expires_at > now()", false).Find(&dbCodes).Error; err != nil {
		return nil, err
	}

	var codes []*model.PairingCode
	for _, pc := range dbCodes {
		codes = append(codes, &model.PairingCode{
			ID:          pc.ID,
			Code:        pc.Code,
			Role:        pc.Role,
			FamilyGroup: pc.FamilyGroup,
			ExpiresAt:   pc.ExpiresAt,
			IsUsed:      pc.IsUsed,
			CreatedAt:   pc.CreatedAt,
		})
	}
	return codes, nil
}

// Mutation returns MutationResolver implementation.
func (r *Resolver) Mutation() MutationResolver { return &mutationResolver{r} }

// Query returns QueryResolver implementation.
func (r *Resolver) Query() QueryResolver { return &queryResolver{r} }

type mutationResolver struct{ *Resolver }
type queryResolver struct{ *Resolver }
