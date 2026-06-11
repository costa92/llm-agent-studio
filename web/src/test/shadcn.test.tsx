import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Button } from '@/components/ui/button'

// Smoke test: proves the RTL + jsdom + shadcn primitive chain renders.
describe('shadcn Button', () => {
  it('renders its children', () => {
    render(<Button>运行</Button>)
    expect(screen.getByRole('button', { name: '运行' })).toBeInTheDocument()
  })
})
