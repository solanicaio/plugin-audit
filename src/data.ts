// The audit data is served on POST only: the host proxies plugin GETs without
// authentication, and requires a signed-in user allowed to use the plugin for
// the other methods.
import { pluginFetch } from './runtime';
import type { Facets, FilterState, ListResponse, Stats } from './types';

const PAGE_SIZE = 100;

async function post<T>(path: string, body: unknown = {}): Promise<T> {
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
  return res.json() as Promise<T>;
}

function toQuery(f: FilterState) {
  return {
    kinds: f.kinds,
    reasons: f.reasons,
    namespaces: f.namespaces,
    actors: f.actors,
    search: f.search.trim() || undefined,
    since: f.since ? new Date(f.since).toISOString() : undefined,
    until: f.until ? new Date(f.until).toISOString() : undefined,
  };
}

export const fetchEvents = (f: FilterState, before?: string) =>
  post<ListResponse>('/api/events/query', { ...toQuery(f), limit: PAGE_SIZE, before });

export const fetchStats = (f: FilterState) => post<Stats>('/api/stats', toQuery(f));

export const fetchFacets = () => post<Facets>('/api/facets');
