import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import ToolsPage from '@/app/tools/page';
import BabyTrackerPage from '@/app/baby/page';
import HousePage from '@/app/house/page';
import FinancePage from '@/app/finance/page';
import AutomationsPage from '@/app/automations/page';
import PreferencesPage from '@/app/preferences/page';
import OverviewPage from '@/app/page';
import MemoryExplorerPage from '@/app/memory/page';
import SharedSpacesPage from '@/app/shared/page';
import AuditRollbackPage from '@/app/audit/page';
import { Sidebar } from '@/components/Sidebar';
import { getMe, graphqlFetch, type Me, type Pack } from '@/lib/graphql';

vi.mock('next/cache', () => ({
  revalidatePath: vi.fn(),
}));

vi.mock('@/lib/graphql', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/graphql')>();
  return {
    ...actual,
    graphqlFetch: vi.fn(async () => ({})),
    getMe: vi.fn(),
    getHouseholdName: vi.fn(async () => 'Test Family'),
  };
});

// The packs as the API lists them (internal/packs): every pack, with its pages.
function packsWith(off: string[]): Pack[] {
  const all: Omit<Pack, 'enabled'>[] = [
    { id: 'baby', name: 'Baby', description: 'Baby care.', pages: ['/baby'] },
    { id: 'house', name: 'House and vehicles', description: 'House.', pages: ['/house'] },
    { id: 'finance', name: 'Finance', description: 'Money.', pages: ['/finance'] },
    { id: 'gmail', name: 'Gmail', description: 'Email.', pages: [] },
    { id: 'automations', name: 'Automations', description: 'Rules.', pages: ['/automations'] },
  ];
  return all.map((p) => ({ ...p, enabled: !off.includes(p.id) }));
}

// The household's topics as the API lists them: Core's, then the topics of the packs that are on.
function topicsWith(off: string[]): string[] {
  const packTopics: Record<string, string> = { baby: 'baby', house: 'house', finance: 'finance' };
  return ['family', 'health', 'personal', 'work', ...Object.keys(packTopics).filter((p) => !off.includes(p))];
}

function meWith(role: string, off: string[] = []): Me {
  return {
    personId: 'p1',
    name: 'Dana',
    role,
    isOperator: false,
    household: { id: 'h1', name: 'Test Family', packs: packsWith(off), topics: topicsWith(off) },
  };
}

const mockedGetMe = vi.mocked(getMe);
const mockedFetch = vi.mocked(graphqlFetch);

beforeEach(() => {
  vi.clearAllMocks();
  mockedFetch.mockImplementation(async () => ({}));
});

// V4-27 done-when: "the placeholder switches on the dashboard Tools page are replaced by real pack
// switches".
describe('Tools & Packs page', () => {
  it('lists every pack with a real switch for an owner, and no placeholder tools', async () => {
    mockedGetMe.mockResolvedValue(meWith('owner', ['baby']));
    const { container } = render(await ToolsPage());

    expect(screen.getByText('Core')).toBeInTheDocument();
    for (const p of packsWith(['baby'])) {
      const card = container.querySelector(`[data-pack="${p.id}"]`) as HTMLElement;
      expect(card).not.toBeNull();
      const button = within(card).getByRole('button');
      expect(button).toHaveTextContent(p.enabled ? `Switch ${p.name} off` : `Switch ${p.name} on`);
      const form = button.closest('form') as HTMLFormElement;
      expect((form.querySelector('input[name="pack"]') as HTMLInputElement).value).toBe(p.id);
      expect((form.querySelector('input[name="enabled"]') as HTMLInputElement).value).toBe(p.enabled ? 'false' : 'true');
    }
    const text = container.textContent ?? '';
    expect(text).not.toMatch(/MCP|Home Assistant|GitHub|Tavily/);
  });

  it('shows the packs without switches to someone who is not an owner', async () => {
    mockedGetMe.mockResolvedValue(meWith('adult'));
    render(await ToolsPage());
    expect(screen.getByText('Finance')).toBeInTheDocument();
    expect(screen.queryAllByRole('button')).toHaveLength(0);
    expect(screen.getByText(/only an owner/i)).toBeInTheDocument();
  });
});

describe('Sidebar', () => {
  it('does not list the pages of a switched-off pack', () => {
    render(<Sidebar me={meWith('owner', ['baby', 'automations'])} />);
    const hrefs = screen.getAllByRole('link').map((a) => a.getAttribute('href'));
    expect(hrefs).not.toContain('/baby');
    expect(hrefs).not.toContain('/automations');
    expect(hrefs).toContain('/finance');
    expect(hrefs).toContain('/house');
    expect(hrefs).toContain('/memory');
    expect(hrefs).toContain('/tools');
    expect(hrefs).toContain('/preferences');
  });

  it('lists every page when nobody is signed in', () => {
    render(<Sidebar me={null} />);
    const hrefs = screen.getAllByRole('link').map((a) => a.getAttribute('href'));
    expect(hrefs).toEqual(expect.arrayContaining(['/baby', '/house', '/finance', '/automations']));
  });
});

// V4-27 done-when: "a household with Baby disabled sees no baby ... page". Each pack's page shows
// that the pack is off and reads none of the pack's data.
describe('Pages of a switched-off pack', () => {
  const cases: [string, () => Promise<React.ReactElement>, string][] = [
    ['baby', BabyTrackerPage, 'Baby'],
    ['house', HousePage, 'House and vehicles'],
    ['finance', FinancePage, 'Finance'],
    ['automations', AutomationsPage, 'Automations'],
  ];
  for (const [id, Page, name] of cases) {
    it(`/${id} shows nothing of the pack while it is off`, async () => {
      mockedGetMe.mockResolvedValue(meWith('owner', [id]));
      const { container } = render(await Page());
      expect(screen.getByText(`The ${name} pack is switched off`)).toBeInTheDocument();
      expect(mockedFetch).not.toHaveBeenCalled();
      if (id === 'baby') {
        expect((container.textContent ?? '').toLowerCase()).not.toContain('baby tracker');
      }
    });
  }

  it('/baby shows the tracker while the pack is on', async () => {
    mockedGetMe.mockResolvedValue(meWith('owner'));
    mockedFetch.mockImplementation(async (query: string) =>
      query.includes('GetBabyData') ? { babyProfiles: [], babyInventory: [] } : { babyCareLogs: [] },
    );
    render(await BabyTrackerPage());
    expect(screen.getByText('Baby & Family Care Tracker')).toBeInTheDocument();
  });
});

// V4-27 done-when: "a household with Automations disabled still has its preferences applied". The
// preferences and the personal CRM left the Automations page for a Core page, which asks for no
// field of any pack, so it works whatever the household has switched off.
describe('Preferences & CRM page', () => {
  it('is Core: it shows the preferences and contacts and reads no pack field', async () => {
    mockedFetch.mockImplementation(async () => ({
      userPreference: {
        userId: 'default', preferredLanguage: 'he', tonePersona: 'concise', defaultCurrency: 'ILS',
        quietHoursStart: '22:00', quietHoursEnd: '07:00', standingInstructions: 'Answer in rhyme.',
      },
      personalCrmContacts: [{ id: '1', personName: 'Grandma Alice', relationship: 'grandmother',
        importantDatesJson: '{}', giftIdeasAndSizes: '', medicalNotes: '', updatedAt: '2026-11-20T00:00:00Z' }],
    }));
    render(await PreferencesPage());
    expect(screen.getByText('Preferences & Personal CRM')).toBeInTheDocument();
    expect(screen.getByDisplayValue('Answer in rhyme.')).toBeInTheDocument();
    expect(screen.getByText('Grandma Alice')).toBeInTheDocument();
    const query = String(mockedFetch.mock.calls[0][0]);
    expect(query).not.toMatch(/automationRules|externalApiConnectors|baby|financial|house/i);
  });

  it('is no longer part of the Automations page', async () => {
    mockedGetMe.mockResolvedValue(meWith('owner'));
    mockedFetch.mockImplementation(async () => ({ externalApiConnectors: [], automationRules: [] }));
    render(await AutomationsPage());
    const query = String(mockedFetch.mock.calls[0][0]);
    expect(query).not.toMatch(/userPreference|personalCrmContacts/);
    expect(screen.queryByText('Personal & Family CRM')).toBeNull();
  });
});

describe('Overview page', () => {
  it('shows no card or text of a switched-off pack', async () => {
    mockedGetMe.mockResolvedValue(meWith('owner', ['baby']));
    const { container } = render(await OverviewPage());
    const hrefs = screen.getAllByRole('link').map((a) => a.getAttribute('href'));
    expect(hrefs).not.toContain('/baby');
    expect(hrefs).toEqual(expect.arrayContaining(['/house', '/finance', '/automations', '/preferences']));
    expect((container.textContent ?? '').toLowerCase()).not.toContain('baby');
    expect(screen.getByText('House and vehicles, Finance, Gmail, Automations')).toBeInTheDocument();
  });

  it('says Core only when every pack is off', async () => {
    mockedGetMe.mockResolvedValue(meWith('owner', ['baby', 'house', 'finance', 'gmail', 'automations']));
    render(await OverviewPage());
    expect(screen.getByText('Core only')).toBeInTheDocument();
  });
});

// V4-27: topics are data. With Baby off no Core page offers a baby topic or names one, and the
// Finance page offers no baby category.
describe('Topic lists', () => {
  const emptyData = async () => ({
    topicMemories: [], entityProfiles: [], sharedSpaces: [], sharedTasks: [], mutationLogs: [],
    financialTransactions: [], categoryBudgets: [], recurringBills: [],
  });

  it('come from the household on the Core pages', async () => {
    mockedGetMe.mockResolvedValue(meWith('owner', ['baby']));
    mockedFetch.mockImplementation(emptyData);
    for (const page of [
      await MemoryExplorerPage({ searchParams: Promise.resolve({}) }),
      await SharedSpacesPage(),
      await AuditRollbackPage({ searchParams: Promise.resolve({}) }),
    ]) {
      const { container, unmount } = render(page);
      expect((container.textContent ?? '').toLowerCase()).not.toContain('baby');
      expect(container.querySelector('option[value="baby"]')).toBeNull();
      expect(container.textContent ?? '').toContain('Health');
      unmount();
    }
  });

  it('offer the baby category on the Finance page only while Baby is on', async () => {
    mockedFetch.mockImplementation(emptyData);
    mockedGetMe.mockResolvedValue(meWith('owner', ['baby']));
    let view = render(await FinancePage());
    expect(view.container.querySelector('option[value="baby"]')).toBeNull();
    expect((view.container.textContent ?? '').toLowerCase()).not.toContain('baby');
    view.unmount();

    mockedGetMe.mockResolvedValue(meWith('owner'));
    view = render(await FinancePage());
    expect(view.container.querySelector('option[value="baby"]')).not.toBeNull();
  });
});
