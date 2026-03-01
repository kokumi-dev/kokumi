import { useEffect, useState } from 'react'
import { getManifest } from '../../api/client'
import type { Preparation } from '../../api/types'
import Modal from '../shared/Modal'
import Btn from '../shared/Btn'
import YamlEditor from '../shared/YamlEditor'

interface Props {
  preparation: Preparation
  onClose: () => void
}

/**
 * ManifestModal fetches the rendered Kubernetes YAML for a Preparation from
 * the OCI artifact and displays it in a read-only CodeMirror editor.
 */
export default function ManifestModal({ preparation: prep, onClose }: Props) {
  const [content, setContent] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    getManifest(prep.namespace, prep.name)
      .then(setContent)
      .catch((e: Error) => setError(e.message))
  }, [prep.namespace, prep.name])

  function copyToClipboard() {
    if (content) void navigator.clipboard.writeText(content)
  }

  const footer = (
    <>
      {content && (
        <Btn variant="secondary" onClick={copyToClipboard}>
          Copy
        </Btn>
      )}
      <Btn variant="secondary" onClick={onClose}>
        Close
      </Btn>
    </>
  )

  return (
    <Modal
      title={`Manifest — ${prep.name}`}
      onClose={onClose}
      footer={footer}
      wide
    >
      {error ? (
        <p style={{ color: '#c0312e', fontSize: '0.875rem' }}>
          Failed to load manifest: {error}
        </p>
      ) : content === null ? (
        <p style={{ color: 'var(--color-text-muted-light)', fontSize: '0.875rem' }}>
          Loading…
        </p>
      ) : (
        <YamlEditor value={content} readOnly tall />
      )}
    </Modal>
  )
}
