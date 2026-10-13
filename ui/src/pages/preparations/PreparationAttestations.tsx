import AttestationPanel from '../../components/preparation/AttestationPanel'
import { usePreparationContext } from './preparationContext'

export default function PreparationAttestations() {
  const { prep } = usePreparationContext()
  return <AttestationPanel key={prep.name} preparation={prep} />
}
