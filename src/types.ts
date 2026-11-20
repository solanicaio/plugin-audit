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

export type Facets = {
  kinds: string[];
  reasons: string[];
  namespaces: string[];
  actors: string[];
};

export type Stats = {
  total: number;
  lastEventAt?: string;
};

export type FilterState = {
  kinds: string[];
  reasons: string[];
  namespaces: string[];
  actors: string[];
  search: string;
  since: string; // datetime-local value
  until: string;
};

export const emptyFilter: FilterState = {
  kinds: [],
  reasons: [],
  namespaces: [],
  actors: [],
  search: '',
  since: '',
  until: '',
};
