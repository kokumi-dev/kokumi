import type { DiffLine } from '../../utils/diff'
import styles from './DiffView.module.css'

interface Props {
  lines: DiffLine[]
}

export default function DiffView({ lines }: Props) {
  if (lines.length === 0) {
    return (
      <p
        style={{
          textAlign: 'center',
          color: 'var(--color-text-muted-light)',
          padding: '24px 0',
          fontSize: '0.875rem',
        }}
      >
        No differences found.
      </p>
    )
  }

  return (
    <div className={styles.diffWrap}>
      <table className={styles.diffTable}>
        <tbody>
          {lines.map((line, i) => (
            <DiffRow key={i} line={line} />
          ))}
        </tbody>
      </table>
    </div>
  )
}

function DiffRow({ line }: { line: DiffLine }) {
  if (line.type === 'omitted') {
    return (
      <tr className={styles.lineOmitted}>
        <td colSpan={3}>{line.content}</td>
      </tr>
    )
  }

  const rowClass =
    line.type === 'added'
      ? styles.lineAdded
      : line.type === 'removed'
        ? styles.lineRemoved
        : ''

  const prefix =
    line.type === 'added' ? '+' : line.type === 'removed' ? '-' : ' '

  return (
    <tr className={rowClass}>
      <td className={styles.lineGutter}>{line.lineNoBefore ?? ''}</td>
      <td className={styles.lineGutter}>{line.lineNoAfter ?? ''}</td>
      <td className={styles.lineContent}>
        <span className={styles.linePrefix}>{prefix}</span>
        {line.content}
      </td>
    </tr>
  )
}
