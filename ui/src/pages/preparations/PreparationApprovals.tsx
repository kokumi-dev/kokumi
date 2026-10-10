import ApprovalPanel from '../../components/preparation/ApprovalPanel'
import { usePreparationContext } from './preparationContext'

export default function PreparationApprovals() {
  const { prep } = usePreparationContext()
  return <ApprovalPanel preparation={prep} />
}
