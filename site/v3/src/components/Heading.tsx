import { useBlurIn } from '../lib/motion'

/** An h2 whose words blur in on scroll. Screen readers get the plain text. */
export function Heading({ id, text, className }: { id?: string; text: string; className?: string }) {
  const ref = useBlurIn<HTMLHeadingElement>()
  const words = text.split(' ')
  return (
    <h2 id={id} ref={ref} className={['split-words', className].filter(Boolean).join(' ')} aria-label={text}>
      {words.map((w, i) => (
        <span key={i} aria-hidden="true">
          <span className="w">{w}</span>
          {i < words.length - 1 ? ' ' : ''}
        </span>
      ))}
    </h2>
  )
}
