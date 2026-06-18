import * as http from 'http';
import type { AddressInfo } from 'net';
import * as path from 'path';
import { ErrorCodes, JsonRpcNotification, JsonRpcRequest, JsonRpcResponse } from './transport';
import { ToolHandler, ToolDefinition, tools as baseTools } from './tools';
import { CodeGraphPackageVersion } from './version';
import CodeGraph from '../index';

const DEFAULT_HOST = '127.0.0.1';
const DEFAULT_ENDPOINT = '/mcp';
const MAX_BODY_BYTES = 1024 * 1024;
const TOOL_SEPARATOR = '__';
const REPOSITORIES_TOOL_NAME = 'codegraph_repos';

export interface MCPGatewayRepository {
  repoId: string;
  url: string;
}

export interface MCPRepositoryRoute {
  repoId: string;
  path: string;
}

export interface MCPGatewayHttpServerOptions {
  repositories: MCPGatewayRepository[];
  host?: string;
  port?: number;
  endpoint?: string;
}

export interface MCPRepositoryRouterHttpServerOptions {
  repositories: MCPRepositoryRoute[];
  host?: string;
  port?: number;
  endpoint?: string;
}

interface MCPTool {
  name: string;
  description?: string;
  inputSchema: unknown;
}

interface ToolsListResult {
  tools?: MCPTool[];
}

export class MCPGatewayHttpServer {
  private server: http.Server | null = null;
  private endpoint: string;
  private host: string;
  private port: number;
  private repositories: MCPGatewayRepository[];

  constructor(options: MCPGatewayHttpServerOptions) {
    this.repositories = options.repositories;
    this.host = options.host ?? DEFAULT_HOST;
    this.port = options.port ?? 0;
    this.endpoint = normalizeEndpoint(options.endpoint ?? DEFAULT_ENDPOINT);
  }

  async start(): Promise<string> {
    this.server = http.createServer((req, res) => {
      void this.handleRequest(req, res);
    });

    await new Promise<void>((resolve, reject) => {
      const server = this.server!;
      const onError = (err: Error) => {
        server.off('listening', onListening);
        reject(err);
      };
      const onListening = () => {
        server.off('error', onError);
        resolve();
      };
      server.once('error', onError);
      server.once('listening', onListening);
      server.listen(this.port, this.host);
    });

    const address = this.server.address() as AddressInfo;
    return `http://${formatHost(this.host)}:${address.port}${this.endpoint}`;
  }

  stop(): void {
    if (this.server) {
      this.server.close();
      this.server = null;
    }
  }

  private async handleRequest(req: http.IncomingMessage, res: http.ServerResponse): Promise<void> {
    if (!this.isEndpoint(req.url)) {
      writeText(res, 404, 'Not Found');
      return;
    }

    if (!originAllowed(req.headers.origin)) {
      writeJson(res, 403, jsonRpcError(null, ErrorCodes.InvalidRequest, 'Forbidden: invalid Origin header'));
      return;
    }

    if (req.method !== 'POST') {
      writeText(res, 405, 'Method Not Allowed', { Allow: 'POST' });
      return;
    }

    if (!acceptsStreamableHttp(req.headers.accept)) {
      writeJson(res, 406, jsonRpcError(null, ErrorCodes.InvalidRequest, 'Not Acceptable: expected application/json and text/event-stream'));
      return;
    }

    let body: string;
    try {
      body = await readBody(req);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      writeJson(res, 413, jsonRpcError(null, ErrorCodes.InvalidRequest, message));
      return;
    }

    const parsed = parseJsonRpcMessage(body);
    if (!parsed.ok) {
      writeJson(res, 400, jsonRpcError(null, parsed.code, parsed.message));
      return;
    }

    if (!('method' in parsed.value) || !('id' in parsed.value)) {
      writeEmpty(res, 202);
      return;
    }

    const response = await this.handleMessage(parsed.value);
    writeJson(res, 200, response);
  }

  private async handleMessage(message: JsonRpcRequest): Promise<JsonRpcResponse> {
    try {
      switch (message.method) {
        case 'initialize':
          return {
            jsonrpc: '2.0',
            id: message.id,
            result: {
              protocolVersion: '2024-11-05',
              capabilities: { tools: {} },
              serverInfo: {
                name: 'codegraph-gateway',
                version: CodeGraphPackageVersion,
              },
              instructions: 'Use repository-prefixed CodeGraph tools exposed by this gateway.',
            },
          };
        case 'tools/list':
          return {
            jsonrpc: '2.0',
            id: message.id,
            result: { tools: await this.listTools() },
          };
        case 'tools/call':
          return await this.callTool(message);
        case 'ping':
          return { jsonrpc: '2.0', id: message.id, result: {} };
        case 'resources/list':
          return { jsonrpc: '2.0', id: message.id, result: { resources: [] } };
        case 'resources/templates/list':
          return { jsonrpc: '2.0', id: message.id, result: { resourceTemplates: [] } };
        case 'prompts/list':
          return { jsonrpc: '2.0', id: message.id, result: { prompts: [] } };
        default:
          return jsonRpcError(message.id, ErrorCodes.MethodNotFound, `Method not found: ${message.method}`);
      }
    } catch (err) {
      return jsonRpcError(message.id, ErrorCodes.InternalError, err instanceof Error ? err.message : String(err));
    }
  }

  private async listTools(): Promise<MCPTool[]> {
    const allTools: MCPTool[] = [];
    for (const repo of this.repositories) {
      try {
        const response = await postJsonRpc(repo.url, {
          jsonrpc: '2.0',
          id: `tools-list-${repo.repoId}`,
          method: 'tools/list',
        });
        if (response.error) continue;
        const result = response.result as ToolsListResult | undefined;
        for (const tool of result?.tools ?? []) {
          allTools.push({
            ...tool,
            name: gatewayToolName(repo.repoId, tool.name),
            description: `[${repo.repoId}] ${tool.description ?? tool.name}`,
          });
        }
      } catch {
        // A down backend should not make the gateway look broken to MCP clients.
      }
    }
    return allTools;
  }

  private async callTool(message: JsonRpcRequest): Promise<JsonRpcResponse> {
    const params = message.params as { name?: unknown; arguments?: Record<string, unknown> } | undefined;
    if (!params || typeof params.name !== 'string') {
      return jsonRpcError(message.id, ErrorCodes.InvalidParams, 'Missing tool name');
    }

    const parsed = parseGatewayToolName(params.name);
    if (!parsed) {
      return jsonRpcError(message.id, ErrorCodes.InvalidParams, `Unknown gateway tool: ${params.name}`);
    }

    const repo = this.repositories.find((candidate) => candidate.repoId === parsed.repoId);
    if (!repo) {
      return jsonRpcError(message.id, ErrorCodes.InvalidParams, `Unknown gateway tool: ${params.name}`);
    }

    const response = await postJsonRpc(repo.url, {
      jsonrpc: '2.0',
      id: message.id,
      method: 'tools/call',
      params: {
        name: parsed.toolName,
        arguments: params.arguments ?? {},
      },
    });
    return { ...response, id: message.id };
  }

  private isEndpoint(rawUrl: string | undefined): boolean {
    if (!rawUrl) return false;
    const path = rawUrl.split('?', 1)[0] || '/';
    return path === this.endpoint;
  }
}

export class MCPRepositoryRouterHttpServer {
  private server: http.Server | null = null;
  private endpoint: string;
  private host: string;
  private port: number;
  private repositories: Map<string, string>;
  private toolHandler = new ToolHandler(null, (projectRoot) => CodeGraph.openSync(projectRoot));

  constructor(options: MCPRepositoryRouterHttpServerOptions) {
    this.repositories = new Map(
      options.repositories.map((repo) => [repo.repoId, path.resolve(repo.path)])
    );
    this.host = options.host ?? DEFAULT_HOST;
    this.port = options.port ?? 0;
    this.endpoint = normalizeEndpoint(options.endpoint ?? DEFAULT_ENDPOINT);
  }

  async start(): Promise<string> {
    this.server = http.createServer((req, res) => {
      void this.handleRequest(req, res);
    });

    await new Promise<void>((resolve, reject) => {
      const server = this.server!;
      const onError = (err: Error) => {
        server.off('listening', onListening);
        reject(err);
      };
      const onListening = () => {
        server.off('error', onError);
        resolve();
      };
      server.once('error', onError);
      server.once('listening', onListening);
      server.listen(this.port, this.host);
    });

    const address = this.server.address() as AddressInfo;
    return `http://${formatHost(this.host)}:${address.port}${this.endpoint}`;
  }

  stop(): void {
    this.toolHandler.closeAll();
    if (this.server) {
      this.server.close();
      this.server = null;
    }
  }

  private async handleRequest(req: http.IncomingMessage, res: http.ServerResponse): Promise<void> {
    if (!this.isEndpoint(req.url)) {
      writeText(res, 404, 'Not Found');
      return;
    }

    if (!originAllowed(req.headers.origin)) {
      writeJson(res, 403, jsonRpcError(null, ErrorCodes.InvalidRequest, 'Forbidden: invalid Origin header'));
      return;
    }

    if (req.method !== 'POST') {
      writeText(res, 405, 'Method Not Allowed', { Allow: 'POST' });
      return;
    }

    if (!acceptsStreamableHttp(req.headers.accept)) {
      writeJson(res, 406, jsonRpcError(null, ErrorCodes.InvalidRequest, 'Not Acceptable: expected application/json and text/event-stream'));
      return;
    }

    let body: string;
    try {
      body = await readBody(req);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      writeJson(res, 413, jsonRpcError(null, ErrorCodes.InvalidRequest, message));
      return;
    }

    const parsed = parseJsonRpcMessage(body);
    if (!parsed.ok) {
      writeJson(res, 400, jsonRpcError(null, parsed.code, parsed.message));
      return;
    }

    if (!('method' in parsed.value) || !('id' in parsed.value)) {
      writeEmpty(res, 202);
      return;
    }

    const response = await this.handleMessage(parsed.value);
    writeJson(res, 200, response);
  }

  private async handleMessage(message: JsonRpcRequest): Promise<JsonRpcResponse> {
    try {
      switch (message.method) {
        case 'initialize':
          return {
            jsonrpc: '2.0',
            id: message.id,
            result: {
              protocolVersion: '2024-11-05',
              capabilities: { tools: {} },
              serverInfo: {
                name: 'codegraph-repository-router',
                version: CodeGraphPackageVersion,
              },
              instructions: 'Use CodeGraph tools with the required repoId argument to query a configured repository.',
            },
          };
        case 'tools/list':
          return {
            jsonrpc: '2.0',
            id: message.id,
            result: { tools: this.listTools() },
          };
        case 'tools/call':
          return await this.callTool(message);
        case 'ping':
          return { jsonrpc: '2.0', id: message.id, result: {} };
        case 'resources/list':
          return { jsonrpc: '2.0', id: message.id, result: { resources: [] } };
        case 'resources/templates/list':
          return { jsonrpc: '2.0', id: message.id, result: { resourceTemplates: [] } };
        case 'prompts/list':
          return { jsonrpc: '2.0', id: message.id, result: { prompts: [] } };
        default:
          return jsonRpcError(message.id, ErrorCodes.MethodNotFound, `Method not found: ${message.method}`);
      }
    } catch (err) {
      return jsonRpcError(message.id, ErrorCodes.InternalError, err instanceof Error ? err.message : String(err));
    }
  }

  private listTools(): ToolDefinition[] {
    const visible = this.toolHandler.getTools();
    const routedTools = visible.map((tool) => ({
      ...tool,
      description: `${tool.description} Requires repoId. Use ${REPOSITORIES_TOOL_NAME} to list configured repositories.`,
      inputSchema: {
        ...tool.inputSchema,
        properties: {
          repoId: {
            type: 'string',
            description: 'Configured repository id to query.',
          },
          ...withoutProjectPath(tool.inputSchema.properties),
        },
        required: [...new Set(['repoId', ...(tool.inputSchema.required ?? [])])],
      },
    }));
    return [...routedTools, repositoriesToolDefinition()];
  }

  private async callTool(message: JsonRpcRequest): Promise<JsonRpcResponse> {
    const params = message.params as { name?: unknown; arguments?: Record<string, unknown> } | undefined;
    if (!params || typeof params.name !== 'string') {
      return jsonRpcError(message.id, ErrorCodes.InvalidParams, 'Missing tool name');
    }
    if (params.name === REPOSITORIES_TOOL_NAME) {
      return {
        jsonrpc: '2.0',
        id: message.id,
        result: {
          content: [{
            type: 'text',
            text: this.formatRepositories(),
          }],
        },
      };
    }
    if (!baseTools.some((tool) => tool.name === params.name)) {
      return jsonRpcError(message.id, ErrorCodes.InvalidParams, `Unknown tool: ${params.name}`);
    }

    const args = { ...(params.arguments ?? {}) };
    const repoId = args.repoId;
    if (typeof repoId !== 'string') {
      return jsonRpcError(message.id, ErrorCodes.InvalidParams, 'Missing required repoId argument');
    }
    const projectPath = this.repositories.get(repoId);
    if (!projectPath) {
      return jsonRpcError(message.id, ErrorCodes.InvalidParams, `Unknown repoId: ${repoId}`);
    }

    delete args.repoId;
    delete args.projectPath;
    const result = await this.toolHandler.execute(params.name, {
      ...args,
      projectPath,
    });
    return { jsonrpc: '2.0', id: message.id, result };
  }

  private formatRepositories(): string {
    if (this.repositories.size === 0) {
      return 'No repositories are configured.';
    }
    return [
      'Configured CodeGraph repositories:',
      '',
      ...[...this.repositories.entries()].map(([repoId, repoPath]) => `- ${repoId}: ${repoPath}`),
    ].join('\n');
  }

  private isEndpoint(rawUrl: string | undefined): boolean {
    if (!rawUrl) return false;
    const requestPath = rawUrl.split('?', 1)[0] || '/';
    return requestPath === this.endpoint;
  }
}

export function parseGatewayRepositories(raw: string): MCPGatewayRepository[] {
  const parsed = JSON.parse(raw) as unknown;
  if (!Array.isArray(parsed)) {
    throw new Error('Gateway repositories must be a JSON array');
  }
  return parsed.map((entry, index) => {
    if (typeof entry !== 'object' || entry === null) {
      throw new Error(`Gateway repository at index ${index} must be an object`);
    }
    const repo = entry as Record<string, unknown>;
    if (typeof repo.repoId !== 'string' || typeof repo.url !== 'string') {
      throw new Error(`Gateway repository at index ${index} must include string repoId and url`);
    }
    return { repoId: repo.repoId, url: repo.url };
  });
}

export function parseRepositoryRoutes(raw: string): MCPRepositoryRoute[] {
  const parsed = JSON.parse(raw) as unknown;
  if (!Array.isArray(parsed)) {
    throw new Error('Repository routes must be a JSON array');
  }
  return parsed.map((entry, index) => {
    if (typeof entry !== 'object' || entry === null) {
      throw new Error(`Repository route at index ${index} must be an object`);
    }
    const repo = entry as Record<string, unknown>;
    if (typeof repo.repoId !== 'string' || typeof repo.path !== 'string') {
      throw new Error(`Repository route at index ${index} must include string repoId and path`);
    }
    return { repoId: repo.repoId, path: repo.path };
  });
}

function withoutProjectPath(properties: ToolDefinition['inputSchema']['properties']): ToolDefinition['inputSchema']['properties'] {
  const { projectPath: _projectPath, ...rest } = properties;
  return rest;
}

function repositoriesToolDefinition(): ToolDefinition {
  return {
    name: REPOSITORIES_TOOL_NAME,
    description: 'List the repository ids configured for this CodeGraph router.',
    inputSchema: {
      type: 'object',
      properties: {},
    },
  };
}

function gatewayToolName(repoId: string, toolName: string): string {
  return `${repoId}${TOOL_SEPARATOR}${toolName}`;
}

function parseGatewayToolName(name: string): { repoId: string; toolName: string } | null {
  const index = name.indexOf(TOOL_SEPARATOR);
  if (index <= 0 || index + TOOL_SEPARATOR.length >= name.length) return null;
  return {
    repoId: name.slice(0, index),
    toolName: name.slice(index + TOOL_SEPARATOR.length),
  };
}

async function postJsonRpc(url: string, message: JsonRpcRequest): Promise<JsonRpcResponse> {
  const response = await fetch(url, {
    method: 'POST',
    headers: {
      accept: 'application/json, text/event-stream',
      'content-type': 'application/json',
    },
    body: JSON.stringify(message),
  });
  if (!response.ok) {
    throw new Error(`Backend ${url} returned HTTP ${response.status}`);
  }
  return await response.json() as JsonRpcResponse;
}

function normalizeEndpoint(endpoint: string): string {
  if (!endpoint.startsWith('/')) return `/${endpoint}`;
  return endpoint;
}

function formatHost(host: string): string {
  return host.includes(':') && !host.startsWith('[') ? `[${host}]` : host;
}

function originAllowed(origin: string | undefined): boolean {
  if (!origin) return true;
  try {
    const url = new URL(origin);
    return ['localhost', '127.0.0.1', '::1', '[::1]'].includes(url.hostname);
  } catch {
    return false;
  }
}

function acceptsStreamableHttp(accept: string | undefined): boolean {
  if (!accept) return false;
  const lower = accept.toLowerCase();
  return lower.includes('application/json') && lower.includes('text/event-stream');
}

function readBody(req: http.IncomingMessage): Promise<string> {
  return new Promise((resolve, reject) => {
    let size = 0;
    const chunks: Buffer[] = [];
    req.on('data', (chunk: Buffer) => {
      size += chunk.length;
      if (size > MAX_BODY_BYTES) {
        reject(new Error('Request body too large'));
        req.destroy();
        return;
      }
      chunks.push(chunk);
    });
    req.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')));
    req.on('error', reject);
  });
}

type ParseResult =
  | { ok: true; value: JsonRpcRequest | JsonRpcNotification | JsonRpcResponse }
  | { ok: false; code: number; message: string };

function parseJsonRpcMessage(body: string): ParseResult {
  let parsed: unknown;
  try {
    parsed = JSON.parse(body);
  } catch {
    return { ok: false, code: ErrorCodes.ParseError, message: 'Parse error: invalid JSON' };
  }

  if (typeof parsed !== 'object' || parsed === null) {
    return { ok: false, code: ErrorCodes.InvalidRequest, message: 'Invalid Request: not a JSON-RPC object' };
  }

  const obj = parsed as Record<string, unknown>;
  if (obj.jsonrpc !== '2.0') {
    return { ok: false, code: ErrorCodes.InvalidRequest, message: 'Invalid Request: not a valid JSON-RPC 2.0 message' };
  }

  if (typeof obj.method === 'string') {
    return { ok: true, value: obj as unknown as JsonRpcRequest | JsonRpcNotification };
  }

  if ('id' in obj && ('result' in obj || 'error' in obj)) {
    return { ok: true, value: obj as unknown as JsonRpcResponse };
  }

  return { ok: false, code: ErrorCodes.InvalidRequest, message: 'Invalid Request: not a valid JSON-RPC 2.0 message' };
}

function jsonRpcError(id: string | number | null, code: number, message: string): JsonRpcResponse {
  return { jsonrpc: '2.0', id, error: { code, message } };
}

function writeJson(res: http.ServerResponse, status: number, body: JsonRpcResponse): void {
  res.writeHead(status, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify(body));
}

function writeEmpty(res: http.ServerResponse, status: number): void {
  res.writeHead(status);
  res.end();
}

function writeText(
  res: http.ServerResponse,
  status: number,
  body: string,
  headers: Record<string, string> = {},
): void {
  res.writeHead(status, { 'Content-Type': 'text/plain; charset=utf-8', ...headers });
  res.end(body);
}
