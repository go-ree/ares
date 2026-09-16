import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  createFetchLogStreamTransport,
  logStreamFailureFromEvent,
  type LogStreamFailure,
  type LogStreamTransport,
} from './log-stream';

const encoder = new TextEncoder();

const waitForEvent = <T extends Event = Event>(target: EventTarget, type: string) =>
  new Promise<T>(resolve =>
    target.addEventListener(type, event => resolve(event as T), { once: true })
  );

const openResponse = (chunks: Uint8Array[]) =>
  new Response(
    new ReadableStream<Uint8Array>({
      start(controller) {
        for (const chunk of chunks) controller.enqueue(chunk);
      },
    }),
    { headers: { 'Content-Type': 'text/event-stream; charset=utf-8' } }
  );

const failureFor = async (transport: LogStreamTransport) => {
  const event = await waitForEvent(transport, 'error');
  return logStreamFailureFromEvent(event);
};

describe('fetch log stream transport', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('uses a credentialed same-origin request and parses split UTF-8 SSE frames', async () => {
    const frame = encoder.encode(
      '\uFEFFevent: log\r\nid: opaque/雪\r\ndata: {"content":"雪","cursor":"opaque/雪","eof":false}\r\n\r\n'
    );
    const crBoundary = frame.indexOf(0x0d) + 1;
    const snowByte = frame.indexOf(0xe9);
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValue(
        openResponse([
          frame.slice(0, crBoundary),
          new Uint8Array(),
          frame.slice(crBoundary, snowByte + 1),
          frame.slice(snowByte + 1),
        ])
      );
    const transport = createFetchLogStreamTransport('/api/v1/tasks/7/steps/build/logs/stream', {
      fetch: fetcher,
    });
    const messagePromise = waitForEvent<MessageEvent<string>>(transport, 'log');

    const message = await messagePromise;

    expect(message.lastEventId).toBe('opaque/雪');
    expect(message.data).toBe('{"content":"雪","cursor":"opaque/雪","eof":false}');
    expect(fetcher).toHaveBeenCalledWith(
      '/api/v1/tasks/7/steps/build/logs/stream',
      expect.objectContaining({
        method: 'GET',
        credentials: 'include',
        redirect: 'error',
        mode: 'same-origin',
        cache: 'no-store',
        referrerPolicy: 'no-referrer',
        headers: { Accept: 'text/event-stream' },
        signal: expect.any(AbortSignal),
      })
    );
    transport.close();
  });

  it('joins multiline data and retains the last event ID like EventSource', async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValue(
        openResponse([
          encoder.encode('id: cursor-1\ndata: first\ndata: second\n\nevent: ping\ndata: {}\n\n'),
        ])
      );
    const transport = createFetchLogStreamTransport('/logs', { fetch: fetcher });
    const messagePromise = waitForEvent<MessageEvent<string>>(transport, 'message');
    const pingPromise = waitForEvent<MessageEvent<string>>(transport, 'ping');

    const [message, ping] = await Promise.all([messagePromise, pingPromise]);

    expect(message.data).toBe('first\nsecond');
    expect(message.lastEventId).toBe('cursor-1');
    expect(ping.lastEventId).toBe('cursor-1');
    transport.close();
  });

  it('accepts the worst-case Go JSON expansion of a maximum-size server chunk', async () => {
    const escapedContent = '\\u003c'.repeat(256 * 1024);
    const frame = encoder.encode(
      `event: log\nid: next\ndata: {"content":"${escapedContent}","cursor":"next","eof":false}\n\n`
    );
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(openResponse([frame]));
    const transport = createFetchLogStreamTransport('/logs', { fetch: fetcher });
    const message = await waitForEvent<MessageEvent<string>>(transport, 'log');

    expect(JSON.parse(message.data)).toEqual({
      content: '<'.repeat(256 * 1024),
      cursor: 'next',
      eof: false,
    });
    transport.close();
  });

  it('surfaces only allowlisted HTTP error metadata and honors Retry-After', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({ error: 'stream_capacity_exceeded', message: 'private detail' }),
        {
          status: 429,
          headers: { 'Content-Type': 'application/json', 'Retry-After': '7' },
        }
      )
    );
    const transport = createFetchLogStreamTransport('/logs', { fetch: fetcher });

    await expect(failureFor(transport)).resolves.toEqual({
      kind: 'http',
      status: 429,
      code: 'stream_capacity_exceeded',
      retryAfterMs: 7000,
    });
  });

  it('replaces untrusted HTTP error bodies with a status-derived stable code', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(JSON.stringify({ error: 'provider-secret', message: 'token=secret' }), {
        status: 502,
        headers: { 'Content-Type': 'application/json' },
      })
    );
    const transport = createFetchLogStreamTransport('/logs', { fetch: fetcher });

    await expect(failureFor(transport)).resolves.toEqual({
      kind: 'http',
      status: 502,
      code: 'upstream_unavailable',
    });
  });

  it('retains the canonical unauthenticated HTTP code', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(JSON.stringify({ error: 'unauthenticated' }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' },
      })
    );
    const transport = createFetchLogStreamTransport('/logs', { fetch: fetcher });

    await expect(failureFor(transport)).resolves.toEqual({
      kind: 'http',
      status: 401,
      code: 'unauthenticated',
    });
  });

  it('retains an oversized HTTP error status when body cancellation rejects', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        new ReadableStream<Uint8Array>({
          cancel() {
            return Promise.reject(new Error('private cancellation detail'));
          },
        }),
        { status: 401, headers: { 'Content-Length': '20000' } }
      )
    );
    const transport = createFetchLogStreamTransport('/logs', { fetch: fetcher });

    await expect(failureFor(transport)).resolves.toEqual({
      kind: 'http',
      status: 401,
      code: 'unauthenticated',
    });
  });

  it.each([
    {
      name: 'non-SSE response',
      response: () => new Response('ok', { headers: { 'Content-Type': 'text/plain' } }),
      maximumFrameBytes: undefined,
    },
    {
      name: 'oversized frame',
      response: () => openResponse([encoder.encode(`data: ${'x'.repeat(64)}\n\n`)]),
      maximumFrameBytes: 32,
    },
    {
      name: 'malformed UTF-8',
      response: () =>
        openResponse([
          new Uint8Array([0x64, 0x61, 0x74, 0x61, 0x3a, 0x20, 0xc3, 0x28, 0x0a, 0x0a]),
        ]),
      maximumFrameBytes: undefined,
    },
    {
      name: 'CRLF frame beyond its exact byte cap',
      response: () => openResponse([encoder.encode('data: ok\r\n\r\n')]),
      maximumFrameBytes: encoder.encode('data: ok\r\n\r\n').byteLength - 1,
    },
  ])('reports $name as a bounded protocol failure', async ({ response, maximumFrameBytes }) => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(response());
    const transport = createFetchLogStreamTransport('/logs', {
      fetch: fetcher,
      maximumFrameBytes,
    });

    await expect(failureFor(transport)).resolves.toEqual({
      kind: 'protocol',
      code: 'invalid_stream_response',
    });
  });

  it('classifies an unexpected clean EOF as a retryable network failure', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(encoder.encode('event: ping\ndata: {}\n\n'), {
        headers: { 'Content-Type': 'text/event-stream' },
      })
    );
    const transport = createFetchLogStreamTransport('/logs', { fetch: fetcher });

    await expect(failureFor(transport)).resolves.toEqual({
      kind: 'network',
      code: 'unexpected_stream_end',
    });
  });

  it('does not start a request or emit an error after being closed', async () => {
    const fetcher = vi.fn<typeof fetch>();
    const transport = createFetchLogStreamTransport('/logs', { fetch: fetcher });
    const error = vi.fn();
    transport.addEventListener('error', error);

    transport.close();
    await Promise.resolve();

    expect(fetcher).not.toHaveBeenCalled();
    expect(error).not.toHaveBeenCalled();
  });

  it('suppresses reader cancellation failures after an intentional close', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        new ReadableStream<Uint8Array>({
          cancel() {
            return Promise.reject(new Error('private cancellation detail'));
          },
        }),
        { headers: { 'Content-Type': 'text/event-stream' } }
      )
    );
    const transport = createFetchLogStreamTransport('/logs', { fetch: fetcher });
    const error = vi.fn();
    transport.addEventListener('error', error);
    await waitForEvent(transport, 'open');

    transport.close();
    await Promise.resolve();
    await Promise.resolve();

    expect(error).not.toHaveBeenCalled();
  });
});
