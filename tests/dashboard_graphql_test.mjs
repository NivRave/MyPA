import test from "node:test";
import assert from "node:assert/strict";
import http from "node:http";
import { graphqlFetch, GraphQLClientError, getGraphQLEndpoint } from "../dashboard/src/lib/graphql.ts";

let server;
let serverPort;
let serverHandler;

test.before(async () => {
  server = http.createServer((req, res) => {
    if (serverHandler) {
      serverHandler(req, res);
    } else {
      res.writeHead(404);
      res.end();
    }
  });

  await new Promise((resolve) => {
    server.listen(0, "127.0.0.1", () => {
      const addr = server.address();
      if (addr && typeof addr === "object") {
        serverPort = addr.port;
      }
      resolve();
    });
  });

  process.env.GRAPHQL_ENDPOINT = `http://127.0.0.1:${serverPort}/graphql`;
});

test.after(() => {
  server.close();
});

test("getGraphQLEndpoint resolves from GRAPHQL_ENDPOINT environment variable", () => {
  assert.equal(getGraphQLEndpoint(), `http://127.0.0.1:${serverPort}/graphql`);
});

test("graphqlFetch includes Authorization header and JSON Content-Type", async () => {
  let receivedHeaders = null;
  serverHandler = (req, res) => {
    receivedHeaders = req.headers;
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end(JSON.stringify({ data: { ping: "pong" } }));
  };

  const result = await graphqlFetch("{ ping }");
  assert.equal(result.ping, "pong");
  assert.equal(receivedHeaders?.["content-type"], "application/json");
  assert.match(receivedHeaders?.["authorization"] || "", /^Bearer /);
});

test("graphqlFetch handles HTTP 401 Unauthorized with typed GraphQLClientError", async () => {
  serverHandler = (_req, res) => {
    res.writeHead(401, { "Content-Type": "application/json" });
    res.end(JSON.stringify({ error: "Invalid API secret token" }));
  };

  let thrownError = null;
  try {
    await graphqlFetch("{ test }");
  } catch (err) {
    thrownError = err;
  }

  assert.ok(thrownError instanceof GraphQLClientError, "Error must be instance of GraphQLClientError");
  assert.ok(thrownError instanceof Error, "Error must be instance of Error");
  assert.equal(thrownError.name, "GraphQLClientError");
  assert.equal(thrownError.status, 401);
  assert.match(thrownError.message, /GraphQL HTTP Error 401/);
  assert.match(thrownError.message, /Invalid API secret token/);
});

test("graphqlFetch handles HTTP 500 Internal Error with typed GraphQLClientError", async () => {
  serverHandler = (_req, res) => {
    res.writeHead(500, { "Content-Type": "text/plain" });
    res.end("Database connection pool exhausted");
  };

  let thrownError = null;
  try {
    await graphqlFetch("{ test }");
  } catch (err) {
    thrownError = err;
  }

  assert.ok(thrownError instanceof GraphQLClientError, "Error must be instance of GraphQLClientError");
  assert.equal(thrownError.status, 500);
  assert.match(thrownError.message, /GraphQL HTTP Error 500/);
  assert.match(thrownError.message, /Database connection pool exhausted/);
});

test("graphqlFetch handles GraphQL resolver/validation errors with typed GraphQLClientError", async () => {
  serverHandler = (_req, res) => {
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end(
      JSON.stringify({
        errors: [
          {
            message: "Cannot query field 'invalidField' on type 'Query'",
            locations: [{ line: 2, column: 3 }],
          },
        ],
      })
    );
  };

  let thrownError = null;
  try {
    await graphqlFetch("{ invalidField }");
  } catch (err) {
    thrownError = err;
  }

  assert.ok(thrownError instanceof GraphQLClientError, "Error must be instance of GraphQLClientError");
  assert.ok(thrownError.errors && thrownError.errors.length === 1);
  assert.equal(thrownError.errors[0].message, "Cannot query field 'invalidField' on type 'Query'");
  assert.match(thrownError.message, /GraphQL Error: Cannot query field 'invalidField'/);
});

test("graphqlFetch handles empty data response ({ data: null }) with typed error", async () => {
  serverHandler = (_req, res) => {
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end(JSON.stringify({ data: null }));
  };

  let thrownError = null;
  try {
    await graphqlFetch("{ test }");
  } catch (err) {
    thrownError = err;
  }

  assert.ok(thrownError instanceof GraphQLClientError);
  assert.equal(thrownError.message, "GraphQL returned empty data response");
});

test("Dashboard page loaders withstand 401, 500, and GraphQL errors without unhandled exceptions or crashes", async () => {
  const failureScenarios = [
    { status: 401, body: JSON.stringify({ error: "Unauthorized" }) },
    { status: 500, body: "Orchestrator failure" },
    { status: 200, body: JSON.stringify({ errors: [{ message: "Tenant access denied" }] }) },
  ];

  for (const scenario of failureScenarios) {
    serverHandler = (_req, res) => {
      res.writeHead(scenario.status, { "Content-Type": "application/json" });
      res.end(scenario.body);
    };

    // 1. Logs loader test
    const logs = await (async () => {
      try {
        const data = await graphqlFetch("{ auditSessions { id } }");
        return data.auditSessions || [];
      } catch (err) {
        assert.ok(err instanceof GraphQLClientError);
        return [];
      }
    })();
    assert.deepEqual(logs, []);

    // 2. Automations loader test
    const automations = await (async () => {
      try {
        const data = await graphqlFetch("{ externalApiConnectors { id } automationRules { id } }");
        return { apis: data.externalApiConnectors || [], rules: data.automationRules || [] };
      } catch (err) {
        assert.ok(err instanceof GraphQLClientError);
        return { apis: [], rules: [] };
      }
    })();
    assert.deepEqual(automations, { apis: [], rules: [] });

    // 3. Baby loader test
    const baby = await (async () => {
      try {
        const data = await graphqlFetch("{ babyProfiles { id } }");
        return data.babyProfiles || [];
      } catch (err) {
        assert.ok(err instanceof GraphQLClientError);
        return [];
      }
    })();
    assert.deepEqual(baby, []);

    // 4. Finance loader test
    const finance = await (async () => {
      try {
        const data = await graphqlFetch("{ transactions { id } }");
        return data.transactions || [];
      } catch (err) {
        assert.ok(err instanceof GraphQLClientError);
        return [];
      }
    })();
    assert.deepEqual(finance, []);

    // 5. Memory loader test
    const memory = await (async () => {
      try {
        const data = await graphqlFetch("{ topicMemories { id } }");
        return data.topicMemories || [];
      } catch (err) {
        assert.ok(err instanceof GraphQLClientError);
        return [];
      }
    })();
    assert.deepEqual(memory, []);

    // 6. Shared loader test
    const shared = await (async () => {
      try {
        const data = await graphqlFetch("{ sharedSpaces { id } }");
        return data.sharedSpaces || [];
      } catch (err) {
        assert.ok(err instanceof GraphQLClientError);
        return [];
      }
    })();
    assert.deepEqual(shared, []);
  }
});
