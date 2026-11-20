package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/proactive"
	"gorm.io/gorm"
)

// Proactive delivery rows (V4-33): the Postgres proactive.Store. The table holds one person's
// messages; its household comes through the membership, as the identity tables' do.

var _ proactive.Store = (*Client)(nil)

// CreateDelivery inserts a new row.
func (c *Client) CreateDelivery(ctx context.Context, d *models.ProactiveDelivery) error {
	return c.DB.WithContext(ctx).Create(d).Error
}

// SaveDelivery writes every field of the row.
func (c *Client) SaveDelivery(ctx context.Context, d *models.ProactiveDelivery) error {
	return c.DB.WithContext(ctx).Save(d).Error
}

// ClaimDelivery moves the row to sending if it is in one of the states; only one caller wins.
func (c *Client) ClaimDelivery(ctx context.Context, id string, from []string) (bool, error) {
	res := c.DB.WithContext(ctx).Model(&models.ProactiveDelivery{}).
		Where("id = ? AND state IN ?", id, from).
		Updates(map[string]any{"state": models.DeliverySending, "updated_at": time.Now()})
	if res.Error != nil {
		return false, fmt.Errorf("claim delivery: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// HeldDeliveries lists the person's held rows, oldest first.
func (c *Client) HeldDeliveries(ctx context.Context, personID string) ([]models.ProactiveDelivery, error) {
	var out []models.ProactiveDelivery
	err := c.DB.WithContext(ctx).Where("person_id = ? AND state = ?", personID, models.DeliveryHeld).
		Order("created_at, id").Find(&out).Error
	return out, err
}

// DueDeliveries lists the pending rows whose next attempt is due, oldest first.
func (c *Client) DueDeliveries(ctx context.Context, now time.Time) ([]models.ProactiveDelivery, error) {
	var out []models.ProactiveDelivery
	err := c.DB.WithContext(ctx).
		Where("state = ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)", models.DeliveryPending, now).
		Order("created_at, id").Find(&out).Error
	return out, err
}

// DeliveryByRef finds the row whose last send on the channel carried the platform ID ref.
func (c *Client) DeliveryByRef(ctx context.Context, channelName, ref string) (*models.ProactiveDelivery, error) {
	var d models.ProactiveDelivery
	err := c.DB.WithContext(ctx).
		Where("channel = ? AND refs <> '' AND ? = ANY (string_to_array(refs, ','))", channelName, ref).
		Order("updated_at DESC").First(&d).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("delivery by ref: %w", err)
	}
	return &d, nil
}

// ExpireDeliveries ends every held or pending row past its time and returns them.
func (c *Client) ExpireDeliveries(ctx context.Context, now time.Time) ([]models.ProactiveDelivery, error) {
	var out []models.ProactiveDelivery
	err := c.DB.WithContext(ctx).Raw(`
		UPDATE proactive_deliveries SET state = ?, updated_at = ?
		WHERE state IN (?, ?) AND expires_at <= ?
		RETURNING *`, models.DeliveryExpired, now, models.DeliveryHeld, models.DeliveryPending, now).Scan(&out).Error
	if err != nil {
		return nil, fmt.Errorf("expire deliveries: %w", err)
	}
	return out, nil
}

// UnstickDeliveries makes rows left sending since before the time pending again.
func (c *Client) UnstickDeliveries(ctx context.Context, before time.Time) (int64, error) {
	res := c.DB.WithContext(ctx).Model(&models.ProactiveDelivery{}).
		Where("state = ? AND updated_at < ?", models.DeliverySending, before).
		Updates(map[string]any{"state": models.DeliveryPending, "next_attempt_at": nil})
	return res.RowsAffected, res.Error
}

// PurgeDeliveries deletes rows that ended before the time.
func (c *Client) PurgeDeliveries(ctx context.Context, before time.Time) (int64, error) {
	res := c.DB.WithContext(ctx).Where("state IN ? AND updated_at < ?", proactive.Ended, before).
		Delete(&models.ProactiveDelivery{})
	return res.RowsAffected, res.Error
}

// DeliveryCounts counts, for /doctor, the messages of a household's members that wait for their
// person (held, pending or mid-send) and those that expired undelivered since the time.
func (c *Client) DeliveryCounts(ctx context.Context, householdID string, since time.Time) (waiting, expired int64, err error) {
	row := c.DB.WithContext(ctx).Raw(`
		SELECT count(*) FILTER (WHERE d.state IN (?, ?, ?)),
		       count(*) FILTER (WHERE d.state = ? AND d.updated_at >= ?)
		FROM proactive_deliveries d JOIN memberships m ON m.person_id = d.person_id
		WHERE m.household_id = ?`,
		models.DeliveryHeld, models.DeliveryPending, models.DeliverySending, models.DeliveryExpired, since, householdID).Row()
	if err := row.Scan(&waiting, &expired); err != nil {
		return 0, 0, fmt.Errorf("delivery counts: %w", err)
	}
	return waiting, expired, nil
}
