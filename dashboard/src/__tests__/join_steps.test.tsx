import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { JoinSteps } from '@/components/JoinSteps';

// V4-41: an owner handing out a pairing code sees what a new member must do first on a channel
// that needs it (a WhatsApp sandbox's join message), with the text to type shown exactly.
describe('JoinSteps', () => {
  it('shows each channel step with the exact text to send', () => {
    render(
      <JoinSteps
        steps={[{ channel: 'whatsapp', step: 'On WhatsApp, first send `join plenty-tiger` to +14155238886; Twilio answers that you joined.' }]}
      />,
    );
    expect(screen.getByText('Before the code')).toBeInTheDocument();
    expect(screen.getByText('whatsapp:')).toBeInTheDocument();
    const code = screen.getByText('join plenty-tiger');
    expect(code.tagName).toBe('CODE');
    expect(screen.getByTestId('join-steps').textContent).not.toContain('`');
  });

  it('shows nothing when no channel needs a step', () => {
    const { container } = render(<JoinSteps steps={[]} />);
    expect(container.innerHTML).toBe('');
  });
});
