// A 256 KiB server chunk can expand to six JSON bytes per input byte when it
// consists entirely of escaped control/HTML characters. Keep the browser cap
// above that worst case while still bounding one in-flight frame.
export const LOG_STREAM_MAX_FRAME_BYTES = 2 * 1024 * 1024;
export const LOG_STREAM_MAX_ERROR_BODY_BYTES = 16 * 1024;
export const LOG_STREAM_MAX_RETRY_AFTER_MS = 5 * 60 * 1000;

export type LogStreamFailureKind = 'http' | 'network' | 'protocol';

export interface LogStreamFailure {
  kind: LogStreamFailureKind;
  code: string;
  status?: number;
  retryAfterMs?: number;
}

export interface LogStreamTransport extends EventTarget {
  readonly url: string;
  readonly withCredentials: boolean;
  readonly readyState: number;
  onopen: ((event: Event) => void) | null;
  onmessage: ((event: MessageEvent<string>) => void) | null;
  onerror: ((event: Event) => void) | null;
  close(): void;
}

export type LogStreamTransportFactory = (url: string) => LogStreamTransport;

export interface FetchLogStreamOptions {
  fetch?: typeof fetch;
  maximumFrameBytes?: number;
}

const CONNECTING = 0;
const OPEN = 1;
const CLOSED = 2;

const HTTP_ERROR_CODES = new Set([
  'invalid_request',
  'invalid_cursor',
  'cursor_conflict',
  'not_found',
  'step_not_found',
  'task_or_step_not_found',
  'logs_not_ready',
  'log_source_mismatch',
  'legacy_task',
  'logs_not_supported',
  'logs_unsupported',
  'stream_capacity_exceeded',
  'upstream_error',
  'upstream_unavailable',
  'executor_unavailable',
  'invalid_log_chunk',
  'internal_error',
  'unauthorized',
  'unauthenticated',
  'forbidden',
]);

const fallbackHTTPCode = (status: number) => {
  const codes: Record<number, string> = {
    400: 'invalid_request',
    401: 'unauthenticated',
    403: 'forbidden',
    404: 'task_or_step_not_found',
    409: 'log_source_mismatch',
    422: 'logs_unsupported',
    429: 'stream_capacity_exceeded',
    502: 'upstream_unavailable',
    503: 'executor_unavailable',
  };
  return codes[status] || (status >= 500 ? 'internal_error' : 'http_error');
};

const safeRetryAfterMilliseconds = (value: string | null, now = Date.now()) => {
  if (!value) return undefined;
  const trimmed = value.trim();
  let delay: number;
  if (/^\d+$/.test(trimmed)) {
    delay = Number(trimmed) * 1000;
  } else {
    const timestamp = Date.parse(trimmed);
    if (!Number.isFinite(timestamp)) return undefined;
    delay = timestamp - now;
  }
  if (!Number.isFinite(delay)) return undefined;
  return Math.min(LOG_STREAM_MAX_RETRY_AFTER_MS, Math.max(1000, Math.ceil(delay)));
};

const concatBytes = (chunks: Uint8Array[], size: number) => {
  const result = new Uint8Array(size);
  let offset = 0;
  for (const chunk of chunks) {
    result.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return result;
};

const readBoundedResponseText = async (response: Response) => {
  const declaredLength = Number(response.headers.get('Content-Length'));
  if (Number.isFinite(declaredLength) && declaredLength > LOG_STREAM_MAX_ERROR_BODY_BYTES) {
    await response.body?.cancel().catch(() => undefined);
    return '';
  }
  if (!response.body) return '';

  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let size = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > LOG_STREAM_MAX_ERROR_BODY_BYTES) {
        await reader.cancel();
        return '';
      }
      chunks.push(value.slice());
    }
    return new TextDecoder('utf-8', { fatal: true }).decode(concatBytes(chunks, size));
  } catch {
    return '';
  } finally {
    reader.releaseLock();
  }
};

const responseErrorCode = async (response: Response) => {
  const raw = await readBoundedResponseText(response);
  if (!raw) return fallbackHTTPCode(response.status);
  try {
    const parsed: unknown = JSON.parse(raw);
    if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') {
      return fallbackHTTPCode(response.status);
    }
    const error = (parsed as Record<string, unknown>).error;
    return typeof error === 'string' && HTTP_ERROR_CODES.has(error)
      ? error
      : fallbackHTTPCode(response.status);
  } catch {
    return fallbackHTTPCode(response.status);
  }
};

class LogStreamFailureEvent extends Event {
  readonly failure: LogStreamFailure;

  constructor(failure: LogStreamFailure) {
    super('error');
    this.failure = failure;
  }
}

const isFailureKind = (value: unknown): value is LogStreamFailureKind =>
  value === 'http' || value === 'network' || value === 'protocol';

export const logStreamFailureFromEvent = (event: Event): LogStreamFailure | null => {
  const value = (event as Event & { failure?: unknown }).failure;
  if (!value || Array.isArray(value) || typeof value !== 'object') return null;
  const failure = value as Record<string, unknown>;
  if (!isFailureKind(failure.kind) || typeof failure.code !== 'string') return null;
  return {
    kind: failure.kind,
    code: failure.code,
    ...(typeof failure.status === 'number' ? { status: failure.status } : {}),
    ...(typeof failure.retryAfterMs === 'number' ? { retryAfterMs: failure.retryAfterMs } : {}),
  };
};

interface ParsedSSEEvent {
  type: string;
  data: string;
  lastEventId: string;
}

class SSEProtocolError extends Error {}

class BoundedSSEParser {
  private readonly decoder = new TextDecoder('utf-8', { fatal: true });
  private readonly lineBuffer: Uint8Array;
  private lineLength = 0;
  private frameBytes = 0;
  private dataLines: string[] = [];
  private eventType = '';
  private lastEventId = '';
  private pendingCR = false;
  private firstLine = true;

  constructor(
    private readonly onEvent: (event: ParsedSSEEvent) => void,
    private readonly maximumFrameBytes: number
  ) {
    this.lineBuffer = new Uint8Array(maximumFrameBytes);
  }

  feed(chunk: Uint8Array) {
    if (chunk.byteLength === 0) return;
    let start = 0;
    if (this.pendingCR) {
      this.pendingCR = false;
      const hasLF = chunk[0] === 0x0a;
      this.processLineEnding(hasLF ? 2 : 1);
      if (hasLF) start = 1;
    }

    for (let index = start; index < chunk.byteLength; index += 1) {
      const byte = chunk[index];
      if (byte !== 0x0a && byte !== 0x0d) continue;
      this.appendLineBytes(chunk.subarray(start, index));
      if (byte === 0x0d) {
        if (chunk[index + 1] === 0x0a) {
          this.processLineEnding(2);
          index += 1;
        } else if (index + 1 === chunk.byteLength) {
          this.pendingCR = true;
        } else {
          this.processLineEnding(1);
        }
      } else {
        this.processLineEnding(1);
      }
      start = index + 1;
    }
    this.appendLineBytes(chunk.subarray(start));
  }

  finish() {
    if (this.pendingCR) {
      this.pendingCR = false;
      this.processLineEnding(1);
    }
    if (this.lineLength > 0) this.decodeLine();
  }

  private processLineEnding(bytes: number) {
    this.frameBytes += bytes;
    this.assertFrameBound();
    this.processLine();
  }

  private appendLineBytes(value: Uint8Array) {
    if (value.byteLength === 0) return;
    if (
      this.lineLength + value.byteLength > this.maximumFrameBytes ||
      this.frameBytes + value.byteLength > this.maximumFrameBytes
    ) {
      throw new SSEProtocolError('bounded SSE frame exceeded');
    }
    this.lineBuffer.set(value, this.lineLength);
    this.lineLength += value.byteLength;
    this.frameBytes += value.byteLength;
  }

  private assertFrameBound() {
    if (this.frameBytes > this.maximumFrameBytes) {
      throw new SSEProtocolError('bounded SSE frame exceeded');
    }
  }

  private decodeLine() {
    try {
      return this.decoder.decode(this.lineBuffer.subarray(0, this.lineLength));
    } catch {
      throw new SSEProtocolError('invalid SSE UTF-8');
    }
  }

  private processLine() {
    let line = this.decodeLine();
    this.lineLength = 0;
    if (this.firstLine) {
      this.firstLine = false;
      if (line.startsWith('\uFEFF')) line = line.slice(1);
    }
    if (line === '') {
      this.dispatchFrame();
      this.frameBytes = 0;
      return;
    }
    if (line.startsWith(':')) return;

    const separator = line.indexOf(':');
    const field = separator < 0 ? line : line.slice(0, separator);
    let value = separator < 0 ? '' : line.slice(separator + 1);
    if (value.startsWith(' ')) value = value.slice(1);
    switch (field) {
      case 'event':
        this.eventType = value;
        break;
      case 'data':
        this.dataLines.push(value);
        break;
      case 'id':
        if (value.includes('\0')) throw new SSEProtocolError('invalid SSE event id');
        this.lastEventId = value;
        break;
      default:
        break;
    }
  }

  private dispatchFrame() {
    if (this.dataLines.length > 0) {
      this.onEvent({
        type: this.eventType || 'message',
        data: this.dataLines.join('\n'),
        lastEventId: this.lastEventId,
      });
    }
    this.dataLines = [];
    this.eventType = '';
  }
}

class FetchLogStreamTransport extends EventTarget implements LogStreamTransport {
  readonly withCredentials = true;
  readyState = CONNECTING;
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent<string>) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;

  private readonly controller = new AbortController();
  private reader: ReadableStreamDefaultReader<Uint8Array> | null = null;
  private closed = false;

  constructor(
    readonly url: string,
    private readonly fetcher: typeof fetch,
    private readonly maximumFrameBytes: number
  ) {
    super();
    this.addEventListener('open', event => this.onopen?.(event));
    this.addEventListener('message', event => this.onmessage?.(event as MessageEvent<string>));
    this.addEventListener('error', event => this.onerror?.(event));
    queueMicrotask(() => void this.connect());
  }

  close() {
    if (this.closed) return;
    this.closed = true;
    this.readyState = CLOSED;
    this.controller.abort();
    this.cancelReader();
  }

  private async connect() {
    if (this.closed) return;
    try {
      const response = await this.fetcher(this.url, {
        method: 'GET',
        credentials: 'include',
        redirect: 'error',
        mode: 'same-origin',
        cache: 'no-store',
        referrerPolicy: 'no-referrer',
        headers: { Accept: 'text/event-stream' },
        signal: this.controller.signal,
      });
      if (this.closed) return;
      if (!response.ok) {
        const failure: LogStreamFailure = {
          kind: 'http',
          status: response.status,
          code: await responseErrorCode(response),
        };
        const retryAfterMs = safeRetryAfterMilliseconds(response.headers.get('Retry-After'));
        if (retryAfterMs !== undefined) failure.retryAfterMs = retryAfterMs;
        this.fail(failure);
        return;
      }

      const contentType = response.headers.get('Content-Type') || '';
      if (!/^text\/event-stream(?:\s*;|\s*$)/i.test(contentType) || !response.body) {
        await response.body?.cancel().catch(() => undefined);
        this.fail({ kind: 'protocol', code: 'invalid_stream_response' });
        return;
      }

      this.readyState = OPEN;
      this.dispatchEvent(new Event('open'));
      if (this.closed) {
        await response.body.cancel().catch(() => undefined);
        return;
      }
      const parser = new BoundedSSEParser(
        event => this.dispatchParsedEvent(event),
        this.maximumFrameBytes
      );
      const reader = response.body.getReader();
      this.reader = reader;
      try {
        for (;;) {
          const { done, value } = await reader.read();
          if (this.closed) return;
          if (done) {
            parser.finish();
            this.fail({ kind: 'network', code: 'unexpected_stream_end' });
            return;
          }
          parser.feed(value);
        }
      } finally {
        this.reader = null;
        reader.releaseLock();
      }
    } catch (error) {
      if (this.closed || this.controller.signal.aborted) return;
      const protocolFailure = error instanceof SSEProtocolError;
      this.fail({
        kind: protocolFailure ? 'protocol' : 'network',
        code: protocolFailure ? 'invalid_stream_response' : 'network_error',
      });
    }
  }

  private dispatchParsedEvent(event: ParsedSSEEvent) {
    if (this.closed) return;
    this.dispatchEvent(
      new MessageEvent(event.type, {
        data: event.data,
        lastEventId: event.lastEventId,
      })
    );
  }

  private fail(failure: LogStreamFailure) {
    if (this.closed) return;
    this.closed = true;
    this.readyState = CLOSED;
    this.controller.abort();
    this.cancelReader();
    this.dispatchEvent(new LogStreamFailureEvent(failure));
  }

  private cancelReader() {
    if (this.reader) void this.reader.cancel().catch(() => undefined);
  }
}

export const createFetchLogStreamTransport = (
  url: string,
  options: FetchLogStreamOptions = {}
): LogStreamTransport =>
  new FetchLogStreamTransport(
    url,
    options.fetch || globalThis.fetch.bind(globalThis),
    options.maximumFrameBytes || LOG_STREAM_MAX_FRAME_BYTES
  );

export const createLegacyLogStreamTransport: LogStreamTransportFactory = url =>
  new EventSource(url, { withCredentials: true });
