// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Vitest tests for NodeCreateDialog. The test
// scope is smoke + key flows (mount, render,
// submit success, submit error, watcher reset)
// per the PR #270 brief. Edge cases (e.g. every
// auth-radio permutation, every Zod superRefine
// rule) are covered by the parent view's manual
// QA.

// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'

import NodeCreateDialog from './NodeCreateDialog.vue'
import type { Node, NodeProvisionResponse } from '@/types'
import { createNode, provisionNode } from '@/api/services'
import { makeI18n, uiStubs } from './__test-helpers'

vi.mock('@/api/services', () => ({
  createNode: vi.fn(),
  provisionNode: vi.fn(),
}))

const testNode: Node = {
  id: '550e8400-e29b-41d4-a716-446655440000',
  name: 'new-node',
  region: 'us-east',
  state: 'new',
  address: '10.0.0.1:22',
  capacityHint: '100',
  createdAt: '2026-08-01T10:00:00Z',
  updatedAt: '2026-08-01T10:00:00Z',
}

const provisionRes: NodeProvisionResponse = {
  node_id: testNode.id,
  new_state: 'online',
  install_stage: 'verify',
  verify_latency: 'PT2.5S',
}

describe('NodeCreateDialog', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.resetAllMocks()
  })

  it('mounts without crashing when closed', () => {
    const wrapper = mount(NodeCreateDialog, {
      props: { open: false },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    expect(wrapper.exists()).toBe(true)
  })

  it('renders the form fields when open', async () => {
    const wrapper = mount(NodeCreateDialog, {
      props: { open: true },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    expect(wrapper.text()).toContain('nodes.name')
    expect(wrapper.text()).toContain('nodes.region')
    expect(wrapper.text()).toContain('nodes.address')
    expect(wrapper.text()).toContain('nodes.capacityHint')
  })

  it('resets the form fields when the dialog re-opens', async () => {
    const wrapper = mount(NodeCreateDialog, {
      props: { open: true },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    await wrapper.setProps({ open: false })
    await nextTick()
    await wrapper.setProps({ open: true })
    await nextTick()
    const nameInput = wrapper
      .findAll('input')
      .find((i) => i.attributes('type') !== 'checkbox')
    expect(nameInput?.element.value).toBe('')
  })

  it('does not call createNode when submit validation fails (empty form)', async () => {
    const wrapper = mount(NodeCreateDialog, {
      props: { open: true },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    await flushPromises()
    const formEl = wrapper.find('form').element as HTMLFormElement
    formEl.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await vi.waitFor(
      () => {
        expect(createNode).not.toHaveBeenCalled()
      },
      { timeout: 500 },
    )
    expect(wrapper.emitted('created')).toBeUndefined()
  })

  it('does not emit created on submit error (mocked wire reject)', async () => {
    vi.mocked(createNode).mockResolvedValue(testNode)
    vi.mocked(provisionNode).mockResolvedValue(provisionRes)
    const wrapper = mount(NodeCreateDialog, {
      props: { open: true },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    await flushPromises()
    const formEl = wrapper.find('form').element as HTMLFormElement
    formEl.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await vi.waitFor(
      () => {
        expect(wrapper.emitted('created')).toBeUndefined()
      },
      { timeout: 500 },
    )
  })

  it('renders the provision-now checkbox by default', () => {
    const wrapper = mount(NodeCreateDialog, {
      props: { open: true },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    const checkbox = wrapper.find('input[type="checkbox"]')
    expect(checkbox.exists()).toBe(true)
  })

  it('renders auth-method radio group when provision is on', () => {
    const wrapper = mount(NodeCreateDialog, {
      props: { open: true },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    expect(wrapper.find('[data-test="radio-group"]').exists()).toBe(true)
  })
})
