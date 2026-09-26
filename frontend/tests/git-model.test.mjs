import test from 'node:test';
import assert from 'node:assert/strict';
import { createGitDemoState, selectBranch, simulateCommit, toggleStaged } from '../src/components/git/git-model.ts';

const project = { id: 'project-a', name: 'Project A', path: 'C:/work/project-a' };
const sessions = [
  { id: 'session-1234', title: 'Active project task', updatedAt: 'now', messages: 2, workspace: 'project-a' },
  { id: 'session-5678', title: 'Archived task', updatedAt: 'yesterday', messages: 5, workspace: 'project-a', archived: true },
  { id: 'session-9999', title: 'Other project task', updatedAt: 'yesterday', messages: 1, workspace: 'project-b' },
];

test('Git demo snapshot is scoped to active sessions in the selected project', () => {
  const state = createGitDemoState(project, sessions);
  assert.equal(state.worktrees.length, 1);
  assert.equal(state.worktrees[0].sessionId, 'session-1234');
  assert.equal(state.worktrees[0].state, 'changes');
  const branch = state.branches.find((item) => item.sessionId === 'session-1234');
  assert.equal(branch.name, 'lxcode/session-session-1234');
  assert.equal(branch.sessionTitle, 'Active project task');
  assert.equal(branch.current, false);
  assert.equal(state.worktrees[0].branch, branch.name);
  assert.equal(state.changes[0].staged, false);
});

test('every active session has a matching branch and worktree beyond old display limits', () => {
  const activeSessions = Array.from({ length: 6 }, (_, index) => ({
    ...sessions[0],
    id: `session-${index}`,
    title: `Task ${index}`,
  }));
  const state = createGitDemoState(project, [...activeSessions, ...sessions.slice(1)]);
  const sessionBranches = state.branches.filter((branch) => branch.sessionId);

  assert.equal(sessionBranches.length, 6);
  assert.equal(state.worktrees.length, 6);
  for (const branch of sessionBranches) {
    const worktree = state.worktrees.find((item) => item.sessionId === branch.sessionId);
    assert.ok(worktree);
    assert.equal(branch.sessionTitle, worktree.title);
    assert.equal(branch.name, worktree.branch);
    assert.equal(branch.current, false);
  }
});

test('projects with no conversations show no fabricated session worktrees', () => {
  const state = createGitDemoState(project, []);
  assert.equal(state.worktrees.length, 0);
  assert.deepEqual(state.branches.map((branch) => branch.name), ['main', 'feature/git-workbench']);
});

test('staging toggles one change without mutating the original list', () => {
  const state = createGitDemoState(project, sessions);
  const updated = toggleStaged(state.changes, 'frontend/src/App.tsx');
  assert.equal(updated[0].staged, true);
  assert.equal(state.changes[0].staged, false);
  assert.equal(updated[1].staged, true);
});

test('branch selection keeps exactly the selected branch current', () => {
  const branches = createGitDemoState(project, sessions).branches;
  const selected = selectBranch(branches, 'feature/git-workbench');
  assert.deepEqual(selected.filter((branch) => branch.current).map((branch) => branch.name), ['feature/git-workbench']);
  assert.equal(branches.find((branch) => branch.current)?.name, 'main');
});

test('simulated commit requires a message and staged changes', () => {
  const state = createGitDemoState(project, sessions);
  assert.deepEqual(simulateCommit(state.changes, state.commits, '  '), { ok: false, reason: 'empty-message' });
  assert.deepEqual(simulateCommit(state.changes.map((change) => ({ ...change, staged: false })), state.commits, 'commit'), {
    ok: false,
    reason: 'nothing-staged',
  });
});

test('simulated commit moves only staged changes and prepends history', () => {
  const state = createGitDemoState(project, sessions);
  const result = simulateCommit(state.changes, state.commits, '  Add Git workbench  ');
  assert.equal(result.ok, true);
  if (!result.ok) return;
  assert.deepEqual(result.changes.map((change) => change.path), ['frontend/src/App.tsx', 'docs/backend-roadmap.md']);
  assert.equal(result.commits[0].message, 'Add Git workbench');
  assert.equal(result.commits[0].files, 1);
  assert.equal(result.commits[1].id, state.commits[0].id);
});
