import type { Environment } from '../lib/api'

const choices: { label: string; value: Environment[] }[] = [
  { label: 'Production', value: ['production'] },
  { label: 'Preview', value: ['preview'] },
  { label: 'Both', value: ['production', 'preview'] },
  { label: 'Neither', value: [] },
]

// EnvPicker is which environments a bucket or a database serves.
export function EnvPicker({ value, onChange }: { value: Environment[]; onChange: (v: Environment[]) => void }) {
  return (
    <div className="seg" role="radiogroup" aria-label="Serves">
      {choices.map((w) => (
        <button
          key={w.label}
          type="button"
          role="radio"
          aria-checked={w.value.join() === value.join()}
          className={w.value.join() === value.join() ? 'on' : ''}
          onClick={() => onChange(w.value)}
        >
          {w.label}
        </button>
      ))}
    </div>
  )
}
