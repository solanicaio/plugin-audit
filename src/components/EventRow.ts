import { h } from '../runtime';
import { formatTime, reasonColor, sx } from '../styles';
import { Chip, TableCell, TableRow, Typography } from '@mui/material';
import type { AuditEvent } from '../types';

// The kind, and the instance and engine the object belongs to.
function objectCaption(e: AuditEvent): string {
  const parts = [e.resourceKind];
  if (e.instance && e.instance !== e.resourceName) parts.push(e.instance);
  if (e.provider) parts.push(e.provider);
  return parts.filter(Boolean).join(' · ');
}

export function EventRow(props: { event: AuditEvent; onSelect: (e: AuditEvent) => void }): any {
  const { event, onSelect } = props;
  return h(
    TableRow,
    { key: event.id, hover: true, sx: { cursor: 'pointer' }, onClick: () => onSelect(event) },
    h(TableCell, { sx: sx.nowrap }, formatTime(event.occurredAt)),
    h(
      TableCell,
      null,
      h(Chip, {
        label: event.reason || event.type,
        size: 'small',
        color: reasonColor(event.reason, event.severity),
        variant: 'outlined',
      })
    ),
    h(
      TableCell,
      null,
      h(Typography, { variant: 'body2', sx: { fontWeight: 600 } }, event.resourceName || '—'),
      h(Typography, { variant: 'caption', sx: { color: 'text.secondary' } }, objectCaption(event))
    ),
    h(TableCell, null, event.namespace || '—'),
    h(TableCell, null, event.actorID || '—'),
    h(TableCell, { sx: sx.message, title: event.message }, event.message || '—')
  );
}
