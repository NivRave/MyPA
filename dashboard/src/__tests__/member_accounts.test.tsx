import { describe, it, expect, vi } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import { MemberAccounts } from '@/components/MemberAccounts';

// BL-41: an owner sees each member's accounts by channel and number ending, and can remove any but
// the last one.
describe('MemberAccounts', () => {
  const members = [
    {
      id: 'p-omer',
      name: 'Omer',
      accounts: [
        { id: 'a-tg', personId: 'p-omer', channel: 'telegram', label: 'telegram ending 1002', linkedAt: '2026-11-01T10:00:00Z' },
        { id: 'a-wa', personId: 'p-omer', channel: 'whatsapp', label: 'whatsapp ending 0123', linkedAt: '2026-11-20T10:00:00Z' },
      ],
    },
    {
      id: 'p-noa',
      name: 'Noa',
      accounts: [{ id: 'a-noa', personId: 'p-noa', channel: 'telegram', label: 'telegram ending 1003', linkedAt: '2026-11-01T10:00:00Z' }],
    },
  ];

  it('offers removal for each account of a member who has more than one', () => {
    render(<MemberAccounts members={members} removeAction={vi.fn()} />);
    const omer = screen.getByText('Omer').closest('li')!;
    expect(within(omer).getByText(/whatsapp ending 0123/)).toBeInTheDocument();
    const buttons = within(omer).getAllByRole('button', { name: 'Remove' });
    expect(buttons).toHaveLength(2);
    const form = buttons[1].closest('form')!;
    expect((form.querySelector('input[name="personId"]') as HTMLInputElement).value).toBe('p-omer');
    expect((form.querySelector('input[name="accountId"]') as HTMLInputElement).value).toBe('a-wa');
  });

  it('never offers to remove the last account', () => {
    render(<MemberAccounts members={members} removeAction={vi.fn()} />);
    const noa = screen.getByText('Noa').closest('li')!;
    expect(within(noa).queryByRole('button', { name: 'Remove' })).toBeNull();
    expect(within(noa).getByText('Last account')).toBeInTheDocument();
  });

  it('shows a refusal', () => {
    render(<MemberAccounts members={members} removeAction={vi.fn()} error="that is the person's last account, so it cannot be removed" />);
    expect(screen.getByRole('alert').textContent).toContain('last account');
  });
});
