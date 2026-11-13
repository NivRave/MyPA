import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import FinancePage from '@/app/finance/page';
import HousePage from '@/app/house/page';
import MemoryExplorerPage from '@/app/memory/page';
import SharedSpacesPage from '@/app/shared/page';
import { graphqlFetch } from '@/lib/graphql';

vi.mock('next/cache', () => ({
  revalidatePath: vi.fn(),
}));

vi.mock('@/lib/graphql', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/graphql')>();
  return {
    ...actual,
    graphqlFetch: vi.fn(),
  };
});

describe('Domain Pages Component Tests', () => {
  const mockedGraphqlFetch = vi.mocked(graphqlFetch);

  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe('FinancePage', () => {
    it('renders clean empty state when no transactions or budgets exist', async () => {
      mockedGraphqlFetch.mockResolvedValueOnce({
        financialTransactions: [],
        categoryBudgets: [],
        recurringBills: [],
      });

      const page = await FinancePage();
      render(page);

      expect(screen.getByText('Financial Tracking & Budget Intelligence')).toBeInTheDocument();
      expect(screen.getByText(/No transactions recorded/i)).toBeInTheDocument();
      expect(screen.getByText(/No budgets set/i)).toBeInTheDocument();
      expect(screen.getByText(/No recurring bills tracked/i)).toBeInTheDocument();
    });

    it('renders populated financial transactions and budgets', async () => {
      mockedGraphqlFetch.mockResolvedValueOnce({
        financialTransactions: [
          {
            id: 'tx-1',
            userId: 'usr-1',
            familyGroup: 'Smith Family',
            type: 'expense',
            category: 'Groceries',
            merchant: 'Whole Foods Market',
            amount: 85.5,
            currency: 'USD',
            transactionDate: '2026-11-06T15:00:00Z',
            isShared: true,
            source: 'credit_card',
            notes: 'Weekly pantry restock',
            createdAt: '2026-11-06T15:00:00Z',
          },
        ],
        categoryBudgets: [
          {
            id: 'b-1',
            familyGroup: 'Smith Family',
            category: 'Groceries',
            monthlyLimit: 800,
            currency: 'USD',
            alertThresholdPct: 80,
            createdAt: '2026-11-01T00:00:00Z',
            updatedAt: '2026-11-01T00:00:00Z',
          },
        ],
        recurringBills: [
          {
            id: 'bill-1',
            familyGroup: 'Smith Family',
            name: 'High-speed Fiber Internet',
            category: 'Utilities',
            expectedAmount: 70,
            currency: 'USD',
            billingCycle: 'monthly',
            nextDueDay: 15,
            autoRenew: true,
            createdAt: '2026-11-01T00:00:00Z',
            updatedAt: '2026-11-01T00:00:00Z',
          },
        ],
      });

      const page = await FinancePage();
      render(page);

      expect(screen.getByText('Whole Foods Market')).toBeInTheDocument();
      expect(screen.getByText('Weekly pantry restock')).toBeInTheDocument();
      expect(screen.getByText('High-speed Fiber Internet')).toBeInTheDocument();
      expect(screen.getByText(/\$800\.00/)).toBeInTheDocument();
    });
  });

  describe('HousePage', () => {
    it('renders clean empty state for house maintenance and contracts', async () => {
      mockedGraphqlFetch.mockResolvedValueOnce({
        houseContracts: [],
        homeMaintenanceItems: [],
        vehicleAssets: [],
      });

      const page = await HousePage();
      render(page);

      expect(screen.getByText('House Maintenance, Contracts & Vehicle Vault')).toBeInTheDocument();
      expect(screen.getByText(/No house contracts or warranties stored yet/i)).toBeInTheDocument();
      expect(screen.getByText(/AC filters, boiler checks, and seasonal upkeep schedules will be listed here/i)).toBeInTheDocument();
      expect(screen.getByText(/No vehicles registered/i)).toBeInTheDocument();
    });

    it('renders populated house contracts and maintenance items', async () => {
      mockedGraphqlFetch.mockResolvedValueOnce({
        houseContracts: [
          {
            id: 'hc-1',
            familyGroup: 'Smith Family',
            recordType: 'warranty',
            title: 'HVAC Heat Pump Warranty',
            vendorOrLandlord: 'Carrier Climate Control',
            amount: 4500,
            currency: 'USD',
            expirationOrRenewalDate: '2028-05-15T00:00:00Z',
            noticePeriodDays: 30,
            createdAt: '2026-11-01T00:00:00Z',
          },
        ],
        homeMaintenanceItems: [
          {
            id: 'maint-1',
            familyGroup: 'Smith Family',
            taskName: 'Replace HVAC Air Filters (MERV 13)',
            category: 'HVAC',
            intervalDays: 90,
            lastCompletedAt: '2026-09-01T00:00:00Z',
            nextDueAt: '2026-12-01T00:00:00Z',
            contractorInfo: 'Self-service',
            notes: '20x25x4 filter in furnace plenum',
          },
        ],
        vehicleAssets: [
          {
            id: 'veh-1',
            familyGroup: 'Smith Family',
            name: 'Family SUV',
            licensePlate: '7ABC123',
            insuranceExpiry: '2027-04-01T00:00:00Z',
            annualTestExpiry: '2027-06-01T00:00:00Z',
            currentMileageKm: 42000,
            nextServiceMileageKm: 48000,
            notes: 'Synthetic 0W-20 oil only',
          },
        ],
      });

      const page = await HousePage();
      render(page);

      expect(screen.getByText('HVAC Heat Pump Warranty')).toBeInTheDocument();
      expect(screen.getByText('Carrier Climate Control')).toBeInTheDocument();
      expect(screen.getByText('Replace HVAC Air Filters (MERV 13)')).toBeInTheDocument();
      expect(screen.getByText('Family SUV')).toBeInTheDocument();
      expect(screen.getByText('7ABC123')).toBeInTheDocument();
    });
  });

  describe('MemoryExplorerPage', () => {
    it('renders empty memory exploration state', async () => {
      mockedGraphqlFetch.mockResolvedValueOnce({
        topicMemories: [],
        entityProfiles: [],
      });

      const page = await MemoryExplorerPage({ searchParams: Promise.resolve({ topic: 'all' }) });
      render(page);

      expect(screen.getByText('Memory Explorer')).toBeInTheDocument();
      expect(screen.getByText(/No topic memories found/i)).toBeInTheDocument();
    });

    it('renders topic memories in ledger table', async () => {
      mockedGraphqlFetch.mockResolvedValueOnce({
        topicMemories: [
          {
            id: 'mem-1',
            userId: 'usr-1',
            familyGroup: 'Smith Family',
            topic: 'health',
            subtopic: 'pediatric',
            fact: 'Maya has a mild sensitivity to wool fabrics',
            tagsJson: '["allergies", "fabrics"]',
            scope: 'family',
            isPinned: true,
            supersededById: null,
            validFrom: '2026-11-01T00:00:00Z',
            validUntil: null,
            createdAt: '2026-11-01T00:00:00Z',
          },
        ],
        entityProfiles: [],
      });

      const page = await MemoryExplorerPage({ searchParams: Promise.resolve({ topic: 'health' }) });
      render(page);

      expect(screen.getByText('Maya has a mild sensitivity to wool fabrics')).toBeInTheDocument();
      expect(screen.getByText('pediatric')).toBeInTheDocument();
    });
  });

  describe('SharedSpacesPage', () => {
    it('renders empty shared spaces and tasks state', async () => {
      mockedGraphqlFetch.mockResolvedValueOnce({
        sharedSpaces: [],
        sharedTasks: [],
      });

      const page = await SharedSpacesPage();
      render(page);

      expect(screen.getByText('Shared Spaces & Partner Sync')).toBeInTheDocument();
      expect(screen.getByText(/No custom rules defined/i)).toBeInTheDocument();
      expect(screen.getByText(/No active shared tasks/i)).toBeInTheDocument();
    });

    it('renders active shared spaces and task assignments', async () => {
      mockedGraphqlFetch.mockResolvedValueOnce({
        sharedSpaces: [
          {
            id: 'ss-1',
            familyGroup: 'Smith Family',
            topic: 'baby',
            ownerUserId: 'usr-parent-1',
            sharedWithUserId: 'usr-partner-2',
            canView: true,
            canEdit: true,
            notifyMode: 'instant',
            updatedAt: '2026-11-01T00:00:00Z',
          },
        ],
        sharedTasks: [
          {
            id: 'st-1',
            familyGroup: 'Smith Family',
            topic: 'baby',
            title: 'Pick up Vitamin D drops from pharmacy',
            dueTime: '2026-11-08T16:00:00Z',
            createdByUserId: 'usr-parent-1',
            assignedToUserId: 'usr-partner-2',
            isCompleted: false,
            completedByUserId: null,
            completedAt: null,
            createdAt: '2026-11-07T12:00:00Z',
          },
        ],
      });

      const page = await SharedSpacesPage();
      render(page);

      expect(screen.getByText('Pick up Vitamin D drops from pharmacy')).toBeInTheDocument();
      const partnerElements = screen.getAllByText('usr-partner-2');
      expect(partnerElements.length).toBeGreaterThanOrEqual(1);
    });
  });
});
