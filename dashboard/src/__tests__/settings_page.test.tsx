import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import SettingsPage from '@/app/settings/page';

// V4-09: the default chat model is defined in one place, on the server
// (config.DefaultGeminiModel). The dashboard must not carry a second, hand-written copy of it:
// it used to show "Gemini 2.5 Flash" as the selected model in a dropdown that did nothing.
describe('Settings page', () => {
  it('names no chat model of its own', () => {
    const { container } = render(<SettingsPage />);
    expect(container.textContent ?? '').not.toMatch(/gemini[- ]?\d/i);
  });

  it('does not offer a model selector that changes nothing', () => {
    render(<SettingsPage />);
    expect(screen.queryByRole('combobox')).toBeNull();
  });

  it('says where the model is set', () => {
    render(<SettingsPage />);
    expect(screen.getByText('Chat model')).toBeInTheDocument();
    expect(screen.getByText('GEMINI_MODEL')).toBeInTheDocument();
    // V4-02: the provider is chosen on the server too.
    expect(screen.getByText('LLM_PROVIDER')).toBeInTheDocument();
    expect(screen.getByText(/built-in default/i)).toBeInTheDocument();
  });
});
