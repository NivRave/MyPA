import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import ErrorBoundary from '@/app/error';

describe('ErrorBoundary component', () => {
  let consoleErrorSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
  });

  afterEach(() => {
    consoleErrorSpy.mockRestore();
  });

  it('renders heading, error message, and Try Again button', () => {
    const error = new Error('Database connection failed') as Error & { digest?: string };
    error.digest = 'ERR_DB_123';
    const reset = vi.fn();

    render(<ErrorBoundary error={error} reset={reset} />);

    expect(screen.getByText('Something went wrong')).toBeInTheDocument();
    expect(screen.getByText('Database connection failed')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /try again/i })).toBeInTheDocument();
  });

  it('renders fallback error text when error message is empty', () => {
    const error = new Error('') as Error & { digest?: string };
    const reset = vi.fn();

    render(<ErrorBoundary error={error} reset={reset} />);

    expect(
      screen.getByText('An unexpected error occurred while loading this view.')
    ).toBeInTheDocument();
  });

  it('calls reset callback when Try Again button is clicked', () => {
    const error = new Error('Network timeout') as Error & { digest?: string };
    const reset = vi.fn();

    render(<ErrorBoundary error={error} reset={reset} />);

    const button = screen.getByRole('button', { name: /try again/i });
    fireEvent.click(button);

    expect(reset).toHaveBeenCalledTimes(1);
  });

  it('logs the unhandled error to console.error via useEffect', () => {
    const error = new Error('Severe schema discrepancy') as Error & { digest?: string };
    const reset = vi.fn();

    render(<ErrorBoundary error={error} reset={reset} />);

    expect(consoleErrorSpy).toHaveBeenCalledWith(
      'Unhandled dashboard route error:',
      error
    );
  });
});
