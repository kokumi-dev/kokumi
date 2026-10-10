import { useEffect, useState } from 'react'
import PageHeader from './PageHeader'
import Section from './Section'
import EmptyState from './EmptyState'
import { ButtonLink } from '../ui'
import styles from './layout.module.css'

interface Props {
  kind: string
  listPath: string
  listLabel: string
  namespace?: string
  name?: string
  loading: boolean
}

/** Placeholder for detail pages while loading or when the resource is gone. */
export default function ResourceNotFound({ kind, listPath, listLabel, namespace, name, loading }: Props) {
  // Freshly created resources reach the live list a moment after the redirect.
  const [graceOver, setGraceOver] = useState(false)
  useEffect(() => {
    const t = setTimeout(() => setGraceOver(true), 3000)
    return () => clearTimeout(t)
  }, [])
  const pending = loading || !graceOver

  return (
    <div className={styles.page}>
      <PageHeader
        breadcrumbs={[{ label: listLabel, to: listPath }, { label: namespace ?? '' }]}
        title={name ?? kind}
      />
      <Section>
        <EmptyState
          text={pending ? 'Loading…' : `${kind} ${namespace}/${name} was not found. It may have been deleted.`}
          action={pending ? undefined : <ButtonLink to={listPath} variant="secondary" size="sm">Back to {listLabel}</ButtonLink>}
        />
      </Section>
    </div>
  )
}
