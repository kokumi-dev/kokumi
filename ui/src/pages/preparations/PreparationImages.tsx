import ImagesPanel from '../../components/preparation/ImagesPanel'
import { usePreparationContext } from './preparationContext'

export default function PreparationImages() {
  const { prep } = usePreparationContext()
  return <ImagesPanel key={prep.name} preparation={prep} />
}
