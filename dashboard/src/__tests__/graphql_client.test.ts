import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  graphqlFetch,
  getGraphQLEndpoint,
  GraphQLClientError,
} from '@/lib/graphql';

describe('graphql client', () => {
  const originalEnv = process.env;
  const originalFetch = global.fetch;

  beforeEach(() => {
    vi.resetModules();
    process.env = { ...originalEnv };
  });

  afterEach(() => {
    process.env = originalEnv;
    global.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  describe('getGraphQLEndpoint', () => {
    it('uses GRAPHQL_ENDPOINT environment variable if defined', () => {
      process.env.GRAPHQL_ENDPOINT = 'http://custom-host:9000/graphql';
      expect(getGraphQLEndpoint()).toBe('http://custom-host:9000/graphql');
    });

    it('uses PROXY_URL environment variable if GRAPHQL_ENDPOINT is not set', () => {
      delete process.env.GRAPHQL_ENDPOINT;
      process.env.PROXY_URL = 'http://proxy-server:8080/';
      expect(getGraphQLEndpoint()).toBe('http://proxy-server:8080/graphql');
    });

    it('uses NEXT_PUBLIC_PROXY_URL if higher priority env vars are unset', () => {
      delete process.env.GRAPHQL_ENDPOINT;
      delete process.env.PROXY_URL;
      process.env.NEXT_PUBLIC_PROXY_URL = 'http://public-proxy:8000';
      expect(getGraphQLEndpoint()).toBe('http://public-proxy:8000/graphql');
    });

    it('falls back to container DNS http://proxy:8000/graphql in server environment', () => {
      delete process.env.GRAPHQL_ENDPOINT;
      delete process.env.PROXY_URL;
      delete process.env.NEXT_PUBLIC_PROXY_URL;

      const originalWindow = global.window;
      // @ts-expect-error testing window deletion
      delete global.window;

      expect(getGraphQLEndpoint()).toBe('http://proxy:8000/graphql');

      global.window = originalWindow;
    });

    it('falls back to http://localhost:8000/graphql when window is defined in browser', () => {
      delete process.env.GRAPHQL_ENDPOINT;
      delete process.env.PROXY_URL;
      delete process.env.NEXT_PUBLIC_PROXY_URL;

      // In jsdom environment, window is defined
      expect(getGraphQLEndpoint()).toBe('http://localhost:8000/graphql');
    });
  });

  describe('graphqlFetch', () => {
    it('attaches default Authorization bearer header and sends query and variables', async () => {
      delete process.env.DASHBOARD_API_SECRET;
      process.env.GRAPHQL_ENDPOINT = 'http://test-server/graphql';

      const mockResponseData = { babyProfiles: [{ id: 'baby-1', name: 'Maya' }] };

      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: async () => ({ data: mockResponseData }),
      });
      global.fetch = mockFetch;

      const query = 'query GetBabies { babyProfiles { id name } }';
      const variables = { familyGroup: 'Smith Family' };

      const result = await graphqlFetch(query, variables);

      expect(result).toEqual(mockResponseData);
      expect(mockFetch).toHaveBeenCalledTimes(1);

      const [url, options] = mockFetch.mock.calls[0];
      expect(url).toBe('http://test-server/graphql');
      expect(options.method).toBe('POST');
      expect(options.headers).toEqual(
        expect.objectContaining({
          'Content-Type': 'application/json',
          Authorization: 'Bearer dev-dashboard-token',
        })
      );
      expect(options.body).toBe(JSON.stringify({ query, variables }));
      expect(options.cache).toBe('no-store');
    });

    it('attaches DASHBOARD_API_SECRET token when configured in environment', async () => {
      process.env.DASHBOARD_API_SECRET = 'super-secret-dashboard-key-1234';
      process.env.GRAPHQL_ENDPOINT = 'http://test-server/graphql';

      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: async () => ({ data: { ping: 'pong' } }),
      });
      global.fetch = mockFetch;

      await graphqlFetch('query { ping }');

      const [, options] = mockFetch.mock.calls[0];
      expect(options.headers.Authorization).toBe('Bearer super-secret-dashboard-key-1234');
    });

    it('merges custom headers and allows overriding options', async () => {
      process.env.GRAPHQL_ENDPOINT = 'http://test-server/graphql';

      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: async () => ({ data: { status: 'ok' } }),
      });
      global.fetch = mockFetch;

      await graphqlFetch(
        'query { status }',
        undefined,
        {
          headers: {
            'X-Custom-Tenant': 'tenant-999',
          },
        }
      );

      const [, options] = mockFetch.mock.calls[0];
      expect(options.headers['X-Custom-Tenant']).toBe('tenant-999');
      expect(options.headers['Content-Type']).toBe('application/json');
    });

    it('throws GraphQLClientError on HTTP error status (401/500)', async () => {
      process.env.GRAPHQL_ENDPOINT = 'http://test-server/graphql';

      const mockFetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        text: async () => 'Missing or invalid token',
      });
      global.fetch = mockFetch;

      await expect(graphqlFetch('query { me { id } }')).rejects.toThrow(GraphQLClientError);

      try {
        await graphqlFetch('query { me { id } }');
      } catch (err) {
        expect(err).toBeInstanceOf(GraphQLClientError);
        const clientErr = err as GraphQLClientError;
        expect(clientErr.status).toBe(401);
        expect(clientErr.message).toContain('GraphQL HTTP Error 401: Missing or invalid token');
      }
    });

    it('throws GraphQLClientError when response contains GraphQL errors array', async () => {
      process.env.GRAPHQL_ENDPOINT = 'http://test-server/graphql';

      const mockErrors = [
        { message: 'Field "unknownField" does not exist' },
        { message: 'Variable $familyGroup is required' },
      ];

      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: async () => ({
          errors: mockErrors,
        }),
      });
      global.fetch = mockFetch;

      await expect(graphqlFetch('query { broken }')).rejects.toThrow(GraphQLClientError);

      try {
        await graphqlFetch('query { broken }');
      } catch (err) {
        expect(err).toBeInstanceOf(GraphQLClientError);
        const clientErr = err as GraphQLClientError;
        expect(clientErr.message).toBe(
          'GraphQL Error: Field "unknownField" does not exist; Variable $familyGroup is required'
        );
        expect(clientErr.errors).toEqual(mockErrors);
      }
    });

    it('throws GraphQLClientError when data is null or undefined', async () => {
      process.env.GRAPHQL_ENDPOINT = 'http://test-server/graphql';

      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: async () => ({ data: null }),
      });
      global.fetch = mockFetch;

      await expect(graphqlFetch('query { empty }')).rejects.toThrow(
        'GraphQL returned empty data response'
      );
    });
  });

  describe('GraphQLClientError class', () => {
    it('correctly sets name, message, errors, and status', () => {
      const errors = [{ message: 'Something failed' }];
      const error = new GraphQLClientError('Test message', errors, 400);

      expect(error.name).toBe('GraphQLClientError');
      expect(error.message).toBe('Test message');
      expect(error.errors).toEqual(errors);
      expect(error.status).toBe(400);
      expect(error instanceof Error).toBe(true);
    });
  });
});
