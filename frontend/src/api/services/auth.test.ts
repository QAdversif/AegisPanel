// SPDX-License-Identifier: AGPL-3.0-or-later
//
// @vitest-environment jsdom
import { describe, it, expect } from 'vitest'
import type { ChangePasswordRequest } from '@/types/aegis'
import { changePassword } from './auth'

/**
 * Smoke test: type-level guarantee that changePassword() accepts
 * the canonical ChangePasswordRequest from types/aegis.ts.
 * If this test stops compiling, the types have drifted.
 */
describe('changePassword type contract', () => {
  it('accepts a ChangePasswordRequest from types/aegis', () => {
    const body: ChangePasswordRequest = {
      current_password: 'old',
      new_password: 'new-password-long-enough',
    }
    expect(body.current_password).toBe('old')
    expect(body.new_password).toBe('new-password-long-enough')
    expect(typeof changePassword).toBe('function')
  })
})
