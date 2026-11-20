import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import BabyTrackerPage from '@/app/baby/page';
import { graphqlFetch } from '@/lib/graphql';

vi.mock('next/cache', () => ({
  revalidatePath: vi.fn(),
}));

vi.mock('@/lib/graphql', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/graphql')>();
  return {
    ...actual,
    graphqlFetch: vi.fn(),
    // V4-35: the page shows the signed-in person's household name.
    getHouseholdName: vi.fn(async () => 'Smith Family'),
  };
});

describe('BabyTrackerPage component', () => {
  const mockedGraphqlFetch = vi.mocked(graphqlFetch);

  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe('Empty state verification', () => {
    beforeEach(async () => {
      mockedGraphqlFetch.mockImplementation(async (query: string) => {
        if (query.includes('GetBabyData')) {
          return {
            babyProfiles: [],
            babyInventory: [],
          };
        }
        if (query.includes('GetBabyLogs')) {
          return {
            babyCareLogs: [],
          };
        }
        return {};
      });
    });

    it('renders clean empty state when no baby profile exists', async () => {
      const pageJsx = await BabyTrackerPage();
      render(pageJsx);

      expect(screen.getByText('Baby & Family Care Tracker')).toBeInTheDocument();
      expect(screen.getByText('No Baby Profile Registered')).toBeInTheDocument();
      expect(
        screen.getByText(/Register a child profile via chat/i)
      ).toBeInTheDocument();
      expect(screen.getByText('Family: Smith Family')).toBeInTheDocument();
    });

    it('disables care log submission when no baby profile is registered', async () => {
      const pageJsx = await BabyTrackerPage();
      render(pageJsx);

      const submitBtn = screen.getByRole('button', {
        name: /register child profile first/i,
      });
      expect(submitBtn).toBeInTheDocument();
      expect(submitBtn).toBeDisabled();
    });

    it('displays helpful empty state messages for milestones, inventory, and activity logs', async () => {
      const pageJsx = await BabyTrackerPage();
      render(pageJsx);

      expect(
        screen.getByText(
          /Age-specific developmental milestones and vaccination schedules will appear here/i
        )
      ).toBeInTheDocument();

      expect(
        screen.getByText(/Formula, diapers, and Vitamin D drops inventory will appear here/i)
      ).toBeInTheDocument();

      expect(
        screen.getByText(/No care routines logged yet/i)
      ).toBeInTheDocument();
    });

    it('verifies ZERO hardcoded mock data remains (no Liam Smith, no 6.20 kg, no Dr. Cohen)', async () => {
      const pageJsx = await BabyTrackerPage();
      render(pageJsx);

      expect(screen.queryByText(/Liam Smith/i)).toBeNull();
      expect(screen.queryByText(/6\.20\s*kg/i)).toBeNull();
      expect(screen.queryByText(/Dr\.\s*Cohen/i)).toBeNull();
      expect(screen.queryByText(/Amoxicillin/i)).toBeNull();
    });
  });

  describe('Registered baby profile and care data', () => {
    const mockBaby = {
      id: 'baby-uuid-101',
      familyGroup: 'Smith Family',
      name: 'Maya Smith',
      dateOfBirth: '2026-05-10T00:00:00Z',
      gender: 'Female',
      birthWeightGrams: 3200,
      currentWeightGrams: 6500,
      heightCm: 63,
      pediatricianInfo: 'Dr. Sarah Adams',
      allergiesNotes: 'None reported',
      updatedAt: '2026-11-01T00:00:00Z',
    };

    const mockInventory = [
      {
        id: 'inv-item-1',
        familyGroup: 'Smith Family',
        itemName: 'Organic Infant Formula',
        quantityRemaining: 2,
        unit: 'cans',
        lowThreshold: 4,
        updatedAt: '2026-11-01T00:00:00Z',
      },
      {
        id: 'inv-item-2',
        familyGroup: 'Smith Family',
        itemName: 'Diapers Size 2',
        quantityRemaining: 60,
        unit: 'pcs',
        lowThreshold: 20,
        updatedAt: '2026-11-01T00:00:00Z',
      },
    ];

    const mockLogs = [
      {
        id: 'log-001',
        babyId: 'baby-uuid-101',
        familyGroup: 'Smith Family',
        logType: 'feeding',
        valueNumeric: 140,
        unit: 'ml',
        details: 'Full bottle of warm formula, burped twice',
        timestamp: '2026-11-07T08:30:00Z',
        loggedByUserId: 'usr-parent-1',
      },
    ];

    beforeEach(() => {
      mockedGraphqlFetch.mockImplementation(async (query: string) => {
        if (query.includes('GetBabyData')) {
          return {
            babyProfiles: [mockBaby],
            babyInventory: mockInventory,
          };
        }
        if (query.includes('GetBabyLogs')) {
          return {
            babyCareLogs: mockLogs,
          };
        }
        return {};
      });
    });

    it('renders profile card with dynamic child metrics and active submit button', async () => {
      const pageJsx = await BabyTrackerPage();
      render(pageJsx);

      expect(screen.getByText('Maya Smith')).toBeInTheDocument();
      expect(screen.getByText('6.50 kg')).toBeInTheDocument();
      expect(screen.getByText('63 cm')).toBeInTheDocument();
      expect(screen.getByText('Dr. Sarah Adams')).toBeInTheDocument();
      expect(screen.getByText('None reported')).toBeInTheDocument();

      const saveBtn = screen.getByRole('button', { name: /save care log/i });
      expect(saveBtn).toBeInTheDocument();
      expect(saveBtn).toBeEnabled();
    });

    it('renders inventory with stock alert badges and care logs in table', async () => {
      const pageJsx = await BabyTrackerPage();
      render(pageJsx);

      expect(screen.getByText('Organic Infant Formula')).toBeInTheDocument();
      expect(screen.getByText('⚠️ Low Stock')).toBeInTheDocument();
      expect(screen.getByText('Diapers Size 2')).toBeInTheDocument();

      expect(screen.getByText('feeding')).toBeInTheDocument();
      expect(screen.getByText('140 ml')).toBeInTheDocument();
      expect(
        screen.getByText('Full bottle of warm formula, burped twice')
      ).toBeInTheDocument();
      expect(screen.getByText('usr-parent-1')).toBeInTheDocument();
    });
  });

  describe('Graceful degradation on GraphQL failure', () => {
    it('handles query rejection without throwing and displays empty fallback', async () => {
      const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
      mockedGraphqlFetch.mockRejectedValue(new Error('Network connection timeout'));

      const pageJsx = await BabyTrackerPage();
      render(pageJsx);

      expect(screen.getByText('No Baby Profile Registered')).toBeInTheDocument();
      expect(screen.getByRole('button', { name: /register child profile first/i })).toBeDisabled();

      consoleErrorSpy.mockRestore();
    });
  });
});
