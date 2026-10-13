import IntentPanel from '../../components/preparation/IntentPanel'
import { usePreparationContext } from './preparationContext'

export default function PreparationIntent() {
  const { prep } = usePreparationContext()
  return <IntentPanel key={prep.name} preparation={prep} />
}
