// The audit data is served on POST only: the host proxies plugin GETs without
// authentication, and requires a signed-in user allowed to use the plugin for
// the other methods.
import { pluginFetch } from './runtime';
import { resolveRange } from './filters';
import type { Facets, FilterState, Histogram, ListResponse, Stats } from './types';

const PAGE_SIZE = 100;

// The most events an export returns, newest first.
export const MAX_EXPORT = 10000;

async function request(path: string, body: unknown): Promise<Response> {
  const res = await pluginFetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const text = await res.text().catch(() => '');
    let message = text;
    try {
      message = JSON.parse(text).error ?? text;
    } catch {
      // not JSON
    }
    throw new Error(message || `HTTP ${res.status}`);
  }
  return res;
}

const post = async <T>(path: string, body: unknown): Promise<T> =>
  (await request(path, body)).json() as Promise<T>;

// Query is the filter sent to the backend, with the time range resolved.
export type Query = ReturnType<typeof toQuery>;

export function toQuery(f: FilterState, now: number) {
  return {
    providers: f.providers,
    kinds: f.kinds,
    reasons: f.reasons,
    namespaces: f.namespaces,
    actors: f.actors,
    severities: f.severities,
    name: f.name.trim() || undefined,
    search: f.search.trim() || undefined,
    ...resolveRange(f, now),
  };
}

export const fetchEvents = (q: Query, before?: string) =>
  post<ListResponse>('/api/events/query', { ...q, limit: PAGE_SIZE, before });

export const fetchStats = (q: Query) => post<Stats>('/api/stats', q);

export const fetchFacets = (q: Query) => post<Facets>('/api/facets', q);

export const fetchHistogram = (q: Query) => post<Histogram>('/api/histogram', q);

// exportEvents downloads the events matching q as a file.
export async function exportEvents(q: Query, format: 'csv' | 'json'): Promise<void> {
  const res = await request('/api/export', { ...q, format });
  const blob = await res.blob();
  const name =
    /filename="([^"]+)"/.exec(res.headers.get('Content-Disposition') ?? '')?.[1] ?? `audit.${format}`;
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}
