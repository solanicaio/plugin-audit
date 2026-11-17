// Mirrors backend/internal/store.Event.
export type AuditEvent = {
  id: number;
  name?: string;
  cursor: string;
  type: string;
  reason?: string;
  severity?: string;
  occurredAt: string;
  cluster?: string;
  namespace?: string;
  resourceKind?: string;
  resourceName?: string;
  actorType?: string;
  actorID?: string;
  message?: string;
  instance?: string;
  provider?: string;
  // The LifecycleEvent as returned by the OpenEverest events API.
  envelope: LifecycleEvent;
};

export type LifecycleEvent = {
  phase?: { from?: string; to?: string };
  sourceIP?: string;
  [key: string]: unknown;
};

export type ListResponse = {
  items: AuditEvent[];
  next?: string;
};

export type FacetValue = { value: string; count: number };

export type Facets = {
  kinds: FacetValue[];
  reasons: FacetValue[];
  namespaces: FacetValue[];
  actors: FacetValue[];
  providers: FacetValue[];
};

export const emptyFacets: Facets = {
  kinds: [],
  reasons: [],
  namespaces: [],
  actors: [],
  providers: [],
};

export type Stats = {
  total: number;
  lastEventAt?: string;
};

export type Bucket = { start: string; normal: number; warning: number };

export type Histogram = {
  // Bucket size in seconds, 0 when no event matches.
  step: number;
  buckets: Bucket[];
};

export type Severity = 'Normal' | 'Warning';

export type ListKey = 'providers' | 'kinds' | 'reasons' | 'namespaces' | 'actors';

export type FilterState = {
  [K in ListKey]: string[];
} & {
  severities: Severity[];
  // Matches the object or instance names that contain it.
  name: string;
  search: string;
  // A preset of TIME_RANGES, or 'custom' for since and until.
  range: string;
  since: string; // ISO time, for a custom range
  until: string;
};

export const emptyFilter: FilterState = {
  providers: [],
  kinds: [],
  reasons: [],
  namespaces: [],
  actors: [],
  severities: [],
  name: '',
  search: '',
  range: '24h',
  since: '',
  until: '',
};
