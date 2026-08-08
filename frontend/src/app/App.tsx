import type { ReactNode } from 'react'
import { Navigate, Outlet, Route, Routes, useParams } from 'react-router-dom'
import { ApiError } from '@/api/errors'
import { LoginPage } from '@/auth/LoginPage'
import { SessionProvider, useMeQuery, useSession, useSessionExpiry } from '@/auth/session'
import { ErrorBoundary } from '@/components/ErrorBoundary'
import { Loading, EmptyState } from '@/components/ui/primitives'
import { AppShell } from '@/layout/AppShell'
import { ProjectEventsProvider } from '@/events/ProjectEvents'
import { ProjectPicker } from '@/features/project/ProjectPicker'
import { ProjectOverview } from '@/features/project/ProjectOverview'
import { ThreadPage } from '@/features/chat/ThreadPage'
import { PlanPage } from '@/features/plan/PlanPage'
import { ItemDetail } from '@/features/plan/ItemDetail'
import { MyTasks } from '@/features/plan/MyTasks'
import { DiagramList } from '@/features/diagram/DiagramList'
import { DiagramPage } from '@/features/diagram/DiagramPage'
import { DiagramEditor } from '@/features/diagram/DiagramEditor'
import { IngestList } from '@/features/ingest/IngestList'
import { IngestPage } from '@/features/ingest/IngestPage'
import { NotesPage } from '@/features/notes/NotesPage'
import { ProposalsPage } from '@/features/proposals/ProposalsPage'
import { ProposalDetail } from '@/features/proposals/ProposalDetail'
import { AuditPage } from '@/features/audit/AuditPage'
import { SettingsPage } from '@/features/project/SettingsPage'
import { AdminPage } from '@/features/admin/AdminPage'

/**
 * Refuses a project route to a non-member.
 *
 * Membership is the boundary, not role (§7.1, D63) — a developer sees the
 * whole project including its audit trail, and a non-member sees none of it.
 * The backend enforces this on every call; this only avoids rendering a shell
 * around a screenful of 403s.
 */
function RequireMembership({ children }: { children: ReactNode }) {
  const { projectId } = useParams()
  const { isMemberOf, user } = useSession()

  if (!projectId) return <Navigate to="/" replace />
  if (!isMemberOf(projectId) && user.role !== 'admin') {
    return (
      <EmptyState
        title="You are not a member of this project"
        body="Membership is what grants access to a project, including its history. An administrator can add you."
      />
    )
  }
  return <>{children}</>
}

/**
 * The project subtree.
 *
 * The event stream is opened here, once, and every screen underneath shares it
 * (frontend-notes §5: one connection per project, not per view). Opening it
 * per screen would give each one its own replay window and its own idea of
 * what it had missed.
 */
function ProjectRoutes() {
  const { projectId = '' } = useParams()
  return (
    <RequireMembership>
      <ProjectEventsProvider projectId={projectId}>
        <ErrorBoundary what="This project">
          <Outlet />
        </ErrorBoundary>
      </ProjectEventsProvider>
    </RequireMembership>
  )
}

function AuthedApp() {
  useSessionExpiry()

  return (
    <Routes>
      <Route element={<AppShell />}>
        <Route path="/" element={<ProjectPicker />} />
        <Route path="/admin" element={<AdminPage />} />

        <Route path="/p/:projectId" element={<ProjectRoutes />}>
          <Route index element={<ProjectOverview />} />
          <Route path="thread" element={<ThreadPage />} />
          <Route path="plan" element={<PlanPage />} />
          <Route path="plan/:itemId" element={<ItemDetail />} />
          <Route path="mine" element={<MyTasks />} />
          <Route path="diagrams" element={<DiagramList />} />
          <Route path="diagrams/:diagramId" element={<DiagramPage />} />
          <Route path="diagrams/:diagramId/edit" element={<DiagramEditor />} />
          <Route path="ingest" element={<IngestList />} />
          <Route path="ingest/:ingestId" element={<IngestPage />} />
          <Route path="proposals" element={<ProposalsPage />} />
          <Route path="proposals/:proposalId" element={<ProposalDetail />} />
          <Route path="notes" element={<NotesPage />} />
          <Route path="audit" element={<AuditPage />} />
          <Route path="settings" element={<SettingsPage />} />
        </Route>

        <Route path="*" element={<EmptyState title="No such page" />} />
      </Route>
    </Routes>
  )
}

export function App() {
  const me = useMeQuery()

  if (me.isPending) {
    return (
      <div className="flex h-full items-center justify-center">
        <Loading what="your session" />
      </div>
    )
  }

  // Not signed in is an answer, not a failure. Anything else that stopped the
  // session lookup is shown on the login page rather than swallowed, because
  // "the password box does nothing" is the worst version of a backend that is
  // down.
  if (me.error) {
    const signedOut = me.error instanceof ApiError && me.error.isAuth
    return <LoginPage {...(signedOut ? {} : { reason: me.error.message })} />
  }

  return (
    <SessionProvider me={me.data}>
      <AuthedApp />
    </SessionProvider>
  )
}
