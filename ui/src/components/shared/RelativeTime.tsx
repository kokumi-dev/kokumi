import { formatDate, formatRelative } from '../../utils/format'

/** Relative timestamp ("3 hours ago") with the absolute date on hover. */
export default function RelativeTime({ iso }: { iso?: string }) {
  if (!iso) return <span>—</span>
  return (
    <time dateTime={iso} title={formatDate(iso)} style={{ whiteSpace: 'nowrap' }}>
      {formatRelative(iso)}
    </time>
  )
}
