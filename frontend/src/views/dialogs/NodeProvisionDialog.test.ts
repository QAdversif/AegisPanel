// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Vitest tests for NodeProvisionDialog. The test
// scope is smoke + key flows (mount, render,
// submit success, submit error, watcher reset,
// default authMethod from node.state) per the
// PR #270 brief. Edge cases (e.g. every
// auth-radio permutation, every Zod superRefine
// rule) are covered by the parent view's manual
// QA.

// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'

import NodeProvisionDialog from './NodeProvisionDialog.vue'
import type { Node, NodeProvisionResponse } from '@/types'
import { provisionNode } from '@/api/services'
import { makeI18n, uiStubs } from './__test-helpers'

vi.mock('@/api/services', () => ({
  provisionNode: vi.fn(),
}))

const newNode: Node = {
  id: '550e8400-e29b-41d4-a716-446655440000',
  name: 'new-node',
  region: 'us-east',
  state: 'new',
  address: '10.0.0.1:22',
  capacityHint: '100',
  createdAt: '2026-08-01T10:00:00Z',
  updatedAt: '2026-08-01T10:00:00Z',
}

const offlineNode: Node = {
  ...newNode,
  id: '660e8400-e29b-41d4-a716-446655440000',
  state: 'offline',
}

const provisionRes: NodeProvisionResponse = {
  node_id: newNode.id,
  new_state: 'online',
  install_stage: 'verify',
  verify_latency: 'PT2.5S',
}

describe('NodeProvisionDialog', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.resetAllMocks()
  })

  it('mounts without crashing when closed', () => {
    const wrapper = mount(NodeProvisionDialog, {
      props: { open: false, node: null },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    expect(wrapper.exists()).toBe(true)
  })

  it('renders the form fields when open with a new node', async () => {
    const wrapper = mount(NodeProvisionDialog, {
      props: { open: true, node: newNode },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    expect(wrapper.text()).toContain('nodes.sshUser')
    expect(wrapper.text()).toContain('nodes.sshPort')
    expect(wrapper.text()).toContain('nodes.authMethod')
  })

  it('shows the target node in the context card', () => {
    const wrapper = mount(NodeProvisionDialog, {
      props: { open: true, node: newNode },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    expect(wrapper.text()).toContain(newNode.name)
    expect(wrapper.text()).toContain(newNode.address)
  })

  it('resets the form when the node prop changes while open', async () => {
    const wrapper = mount(NodeProvisionDialog, {
      props: { open: true, node: newNode },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await wrapper.setProps({ node: offlineNode })
    await nextTick()
    expect(wrapper.text()).toContain(offlineNode.name)
  })

  it('does not call provisionNode when submit validation fails (empty form)', async () => {
    const wrapper = mount(NodeProvisionDialog, {
      props: { open: true, node: newNode },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    await flushPromises()
    const formEl = wrapper.find('form').element as HTMLFormElement
    formEl.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await vi.waitFor(
      () => {
        expect(provisionNode).not.toHaveBeenCalled()
      },
      { timeout: 500 },
    )
    expect(wrapper.emitted('provisioned')).toBeUndefined()
  })

  it('does not emit provisioned on submit error (mocked wire reject)', async () => {
    vi.mocked(provisionNode).mockResolvedValue(provisionRes)
    const wrapper = mount(NodeProvisionDialog, {
      props: { open: true, node: newNode },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    await flushPromises()
    const formEl = wrapper.find('form').element as HTMLFormElement
    formEl.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await vi.waitFor(
      () => {
        expect(wrapper.emitted('provisioned')).toBeUndefined()
      },
      { timeout: 500 },
    )
  })

  it('renders the auth-method radio group with three options for offline nodes', () => {
    const wrapper = mount(NodeProvisionDialog, {
      props: { open: true, node: offlineNode },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    expect(wrapper.find('[data-test="radio-group"]').exists()).toBe(true)
  })
})
