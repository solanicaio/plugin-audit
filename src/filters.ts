// Time ranges, the filter labels, and the filter state kept in the page URL so
// a filtered view can be shared or reloaded.
import { emptyFilter } from './types';
import type { FilterState, ListKey, Severity } from './types';

const MINUTE = 60 * 1000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

export const TIME_RANGES: Array<{ key: string; label: string; ms?: number }> = [
  { key: '15m', label: '15m', ms: 15 * MINUTE },
  { key: '1h', label: '1h', ms: HOUR },
  { key: '24h', label: '24h', ms: DAY },
  { key: '7d', label: '7d', ms: 7 * DAY },
  { key: '30d', label: '30d', ms: 30 * DAY },
  { key: 'all', label: 'All' },
];

export const LIST_FILTERS: Array<{ key: ListKey; label: string; param: string }> = [
  { key: 'providers', label: 'Engine', param: 'engine' },
  { key: 'kinds', label: 'Kind', param: 'kind' },
  { key: 'reasons', label: 'Event', param: 'event' },
  { key: 'namespaces', label: 'Namespace', param: 'namespace' },
  { key: 'actors', label: 'Actor', param: 'actor' },
];

// The since and until of the filter's time range at now.
export function resolveRange(f: FilterState, now: number): { since?: string; until?: string } {
  if (f.range === 'custom') {
    return { since: f.since || undefined, until: f.until || undefined };
  }
  const preset = TIME_RANGES.find((r) => r.key === f.range);
  return preset?.ms ? { since: new Date(now - preset.ms).toISOString() } : {};
}

// Whether anything but the time range is filtered.
export function hasFilters(f: FilterState): boolean {
  return (
    LIST_FILTERS.some(({ key }) => f[key].length > 0) ||
    f.severities.length > 0 ||
    f.name.trim() !== '' ||
    f.search.trim() !== ''
  );
}

const isTime = (v: string | null): v is string => !!v && !Number.isNaN(Date.parse(v));

export function readURL(search: string): FilterState {
  const p = new URLSearchParams(search);
  const f: FilterState = { ...emptyFilter };
  for (const { key, param } of LIST_FILTERS) {
    f[key] = p.getAll(param).filter(Boolean);
  }
  f.severities = p.getAll('severity').filter((s): s is Severity => s === 'Normal' || s === 'Warning');
  f.name = p.get('name') ?? '';
  f.search = p.get('q') ?? '';
  const range = p.get('range');
  if (range === 'custom') {
    const since = p.get('since');
    const until = p.get('until');
    if (isTime(since) || isTime(until)) {
      f.range = 'custom';
      f.since = isTime(since) ? since : '';
      f.until = isTime(until) ? until : '';
    }
  } else if (range && TIME_RANGES.some((r) => r.key === range)) {
    f.range = range;
  }
  return f;
}

export function writeURL(f: FilterState, search: string): string {
  const p = new URLSearchParams(search);
  for (const k of [...LIST_FILTERS.map((l) => l.param), 'severity', 'name', 'q', 'range', 'since', 'until']) {
    p.delete(k);
  }
  for (const { key, param } of LIST_FILTERS) {
    f[key].forEach((v) => p.append(param, v));
  }
  f.severities.forEach((v) => p.append('severity', v));
  if (f.name.trim()) p.set('name', f.name.trim());
  if (f.search.trim()) p.set('q', f.search.trim());
  if (f.range !== emptyFilter.range) p.set('range', f.range);
  if (f.range === 'custom') {
    if (f.since) p.set('since', f.since);
    if (f.until) p.set('until', f.until);
  }
  const s = p.toString();
  return s ? `?${s}` : '';
}
