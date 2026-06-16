import { describe, it, expect, afterEach } from 'vitest';
import { spawn, ChildProcessWithoutNullStreams } from 'child_process';
import * as http from 'http';
import type { AddressInfo } from 'net';
import * as path from 'path';
import { MCPGatewayHttpServer } from '../src/mcp/gateway';
import { WASM_RUNTIME_FLAGS } from '../src/extraction/wasm-runtime-flags';

const BIN = path.resolve(__dirname, '../dist/bin/codegraph.js');

type JsonRpcMessage = {
  jsonrpc: '2.0';
  id?: string | number;
  method?: string;
  params?: {
    name?: string;
    arguments?: Record<string, unknown>;
  };
};

type Backend = {
  url: string;
  calls: JsonRpcMessage[];
  stop: () => Promise<void>;
};

const headers = {
  accept: 'application/json, text/event-stream',
  'content-type': 'application/json',
};

async function startBackend(toolName: string, callText: string): Promise<Backend> {
  const calls: JsonRpcMessage[] = [];
  const server = http.createServer((req, res) => {
    let body = '';
    req.on('data', (chunk: Buffer) => {
      body += chunk.toString('utf8');
    });
    req.on('end', () => {
      const message = JSON.parse(body) as JsonRpcMessage;
      calls.push(message);
      res.writeHead(200, { 'content-type': 'application/json' });
      if (message.method === 'tools/list') {
        res.end(JSON.stringify({
          jsonrpc: '2.0',
          id: message.id,
          result: {
            tools: [{
              name: toolName,
              description: `Tool ${toolName}`,
              inputSchema: { type: 'object', properties: {} },
            }],
          },
        }));
        return;
      }
      if (message.method === 'tools/call') {
        res.end(JSON.stringify({
          jsonrpc: '2.0',
          id: message.id,
          result: {
            content: [{ type: 'text', text: `${callText}:${message.params?.name}` }],
          },
        }));
        return;
      }
      res.end(JSON.stringify({ jsonrpc: '2.0', id: message.id, result: {} }));
    });
  });

  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const address = server.address() as AddressInfo;
  return {
    url: `http://127.0.0.1:${address.port}/mcp`,
    calls,
    stop: () => new Promise((resolve) => server.close(() => resolve())),
  };
}

async function post(url: string, body: JsonRpcMessage): Promise<Response> {
  return fetch(url, {
    method: 'POST',
    headers,
    body: JSON.stringify(body),
  });
}

function waitForHttpUrl(child: ChildProcessWithoutNullStreams): Promise<string> {
  return new Promise((resolve, reject) => {
    let stderr = '';
    const timer = setTimeout(() => {
      cleanup();
      reject(new Error(`timed out waiting for HTTP server URL. stderr=${stderr}`));
    }, 5000);
    const onData = (chunk: Buffer) => {
      stderr += chunk.toString('utf8');
      const match = stderr.match(/listening on (http:\/\/[^\s]+)/);
      if (match?.[1]) {
        cleanup();
        resolve(match[1]);
      }
    };
    const onExit = (code: number | null) => {
      cleanup();
      reject(new Error(`server exited before listening, code=${code}, stderr=${stderr}`));
    };
    const cleanup = () => {
      clearTimeout(timer);
      child.stderr.off('data', onData);
      child.off('exit', onExit);
    };
    child.stderr.on('data', onData);
    child.on('exit', onExit);
  });
}

function stopChild(child: ChildProcessWithoutNullStreams): Promise<void> {
  return new Promise((resolve) => {
    if (child.exitCode !== null || child.signalCode !== null) {
      resolve();
      return;
    }
    const timer = setTimeout(() => {
      child.kill('SIGKILL');
    }, 1000);
    child.once('exit', () => {
      clearTimeout(timer);
      resolve();
    });
    child.kill('SIGTERM');
  });
}

describe('MCP gateway HTTP server', () => {
  const cleanup: Array<() => Promise<void> | void> = [];

  afterEach(async () => {
    while (cleanup.length > 0) {
      await cleanup.pop()?.();
    }
  });

  it('initializes as one gateway MCP server', async () => {
    const gateway = new MCPGatewayHttpServer({ repositories: [] });
    cleanup.push(() => gateway.stop());
    const url = await gateway.start();

    const res = await post(url, {
      jsonrpc: '2.0',
      id: 1,
      method: 'initialize',
      params: {},
    });

    expect(res.status).toBe(200);
    const json = await res.json() as {
      result: { serverInfo: { name: string }; capabilities: { tools: unknown } };
    };
    expect(json.result.serverInfo.name).toBe('codegraph-gateway');
    expect(json.result.capabilities.tools).toBeDefined();
  });

  it('lists backend tools with repository prefixes', async () => {
    const first = await startBackend('codegraph_explore', 'first');
    const second = await startBackend('codegraph_status', 'second');
    cleanup.push(first.stop, second.stop);
    const gateway = new MCPGatewayHttpServer({
      repositories: [
        { repoId: 'hello-1', url: first.url },
        { repoId: 'hello-2', url: second.url },
      ],
    });
    cleanup.push(() => gateway.stop());
    const url = await gateway.start();

    const res = await post(url, {
      jsonrpc: '2.0',
      id: 2,
      method: 'tools/list',
    });

    expect(res.status).toBe(200);
    const json = await res.json() as { result: { tools: Array<{ name: string }> } };
    expect(json.result.tools.map((tool) => tool.name)).toEqual([
      'hello-1__codegraph_explore',
      'hello-2__codegraph_status',
    ]);
  });

  it('dispatches prefixed tool calls to the matching backend with the original tool name', async () => {
    const first = await startBackend('codegraph_explore', 'first');
    const second = await startBackend('codegraph_status', 'second');
    cleanup.push(first.stop, second.stop);
    const gateway = new MCPGatewayHttpServer({
      repositories: [
        { repoId: 'hello-1', url: first.url },
        { repoId: 'hello-2', url: second.url },
      ],
    });
    cleanup.push(() => gateway.stop());
    const url = await gateway.start();

    const res = await post(url, {
      jsonrpc: '2.0',
      id: 3,
      method: 'tools/call',
      params: {
        name: 'hello-2__codegraph_status',
        arguments: { verbose: true },
      },
    });

    expect(res.status).toBe(200);
    const json = await res.json() as { result: { content: Array<{ text: string }> } };
    expect(json.result.content[0]?.text).toBe('second:codegraph_status');
    expect(second.calls.at(-1)?.params?.name).toBe('codegraph_status');
    expect(second.calls.at(-1)?.params?.arguments).toEqual({ verbose: true });
    expect(first.calls.some((call) => call.method === 'tools/call')).toBe(false);
  });

  it('rejects unknown repository tool prefixes', async () => {
    const gateway = new MCPGatewayHttpServer({ repositories: [] });
    cleanup.push(() => gateway.stop());
    const url = await gateway.start();

    const res = await post(url, {
      jsonrpc: '2.0',
      id: 4,
      method: 'tools/call',
      params: { name: 'missing__codegraph_status', arguments: {} },
    });

    expect(res.status).toBe(200);
    const json = await res.json() as { error: { code: number; message: string } };
    expect(json.error.code).toBe(-32602);
    expect(json.error.message).toContain('Unknown gateway tool');
  });

  it('starts the gateway from the serve --mcp --http CLI branch', async () => {
    const backend = await startBackend('codegraph_status', 'backend');
    cleanup.push(backend.stop);
    const child = spawn(
      process.execPath,
      [
        ...WASM_RUNTIME_FLAGS,
        BIN,
        'serve',
        '--mcp',
        '--http',
        '--port',
        '0',
        '--gateway-repos',
        JSON.stringify([{ repoId: 'hello-1', url: backend.url }]),
      ],
      { stdio: ['ignore', 'pipe', 'pipe'] },
    ) as ChildProcessWithoutNullStreams;
    cleanup.push(() => stopChild(child));
    const url = await waitForHttpUrl(child);

    const res = await post(url, {
      jsonrpc: '2.0',
      id: 5,
      method: 'tools/list',
    });

    expect(res.status).toBe(200);
    const json = await res.json() as { result: { tools: Array<{ name: string }> } };
    expect(json.result.tools.map((tool) => tool.name)).toEqual(['hello-1__codegraph_status']);
  }, 10000);
});
