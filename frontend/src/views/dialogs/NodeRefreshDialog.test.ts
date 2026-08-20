// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Vitest tests for NodeRefreshDialog. The test
// scope is smoke + key flows (mount, render,
// fire-and-forget POST on open, success card,
// error state) per the PR #270 brief. Edge cases
// (e.g. retry behaviour) are covered by the
// parent view's manual QA.

// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'

import NodeRefreshDialog from './NodeRefreshDialog.vue'
import type { Node, NodeRefreshAgentBearerResponse } from '@/types'
import { refreshNodeAgentBearer } from '@/api/services'
import { makeI18n, uiStubs } from './__test-helpers'

vi.mock('@/api/services', () => ({
  refreshNodeAgentBearer: vi.fn(),
}))

const testNode: Node = {
  id: '550e8400-e29b-41d4-a716-446655440000',
  name: 'refresh-target',
  region: 'us-east',
  state: 'online',
  address: '10.0.0.1:22',
  capacityHint: '100',
  createdAt: '2026-08-01T10:00:00Z',
  updatedAt: '2026-08-01T10:00:00Z',
}

const refreshRes: NodeRefreshAgentBearerResponse = {
  node_id: testNode.id,
  bearer: 'aegis-bearer-abc123def456',
  key_fingerprint: 'SHA256:abc123def456',
}

describe('NodeRefreshDialog', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.resetAllMocks()
  })

  it('mounts without crashing when closed', () => {
    const wrapper = mount(NodeRefreshDialog, {
      props: { open: false, node: null },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    expect(wrapper.exists()).toBe(true)
  })

  it('fires the POST on open and shows the loading state', async () => {
    // Use a never-resolving promise so the loading
    // state remains visible at assertion time.
    vi.mocked(refreshNodeAgentBearer).mockImplementation(
      () => new Promise(() => {}),
    )
    const wrapper = mount(NodeRefreshDialog, {
      props: { open: true, node: testNode },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    expect(refreshNodeAgentBearer).toHaveBeenCalled()
    expect(wrapper.text()).toContain('nodes.refreshLoading')
  })

  it('shows the success card after the POST resolves', async () => {
    vi.mocked(refreshNodeAgentBearer).mockResolvedValue(refreshRes)
    const wrapper = mount(NodeRefreshDialog, {
      props: { open: true, node: testNode },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    await vi.waitFor(
      () => {
        expect(wrapper.emitted('refreshed')).toBeDefined()
      },
      { timeout: 1000 },
    )
    // The success card's bearer input binds the
    // response's bearer field via the Input stub's
    // `value` attribute (not its text content).
    const inputs = wrapper.findAll('input')
    const bearerInput = inputs.find((i) => i.element.value === refreshRes.bearer)
    expect(bearerInput).toBeDefined()
    const fingerprintInput = inputs.find(
      (i) => i.element.value === refreshRes.key_fingerprint,
    )
    expect(fingerprintInput).toBeDefined()
  })

  it('shows the error state and emits failed on POST reject', async () => {
    vi.mocked(refreshNodeAgentBearer).mockRejectedValue(new Error('boom'))
    const wrapper = mount(NodeRefreshDialog, {
      props: { open: true, node: testNode },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    await vi.waitFor(
      () => {
        expect(wrapper.emitted('failed')).toBeDefined()
      },
      { timeout: 1000 },
    )
    const failedArgs = wrapper.emitted('failed')?.[0]
    expect(failedArgs?.[0]).toEqual(testNode)
    expect(failedArgs?.[1]).toContain('boom')
    expect(wrapper.text()).toContain('boom')
  })

  it('does not call refreshNodeAgentBearer when node is null', async () => {
    mount(NodeRefreshDialog, {
      props: { open: true, node: null },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    await flushPromises()
    expect(refreshNodeAgentBearer).not.toHaveBeenCalled()
  })
})
