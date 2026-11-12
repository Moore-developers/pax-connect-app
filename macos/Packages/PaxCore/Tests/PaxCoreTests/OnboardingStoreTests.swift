import Foundation
import Testing

@testable import PaxCore

@MainActor struct OnboardingStoreTests {
  private func preferences() throws -> UserDefaults {
    let name = "OnboardingTests." + UUID().uuidString
    return try #require(UserDefaults(suiteName: name))
  }
  @Test func firstIdentityAndResume() throws {
    let defaults = try preferences()
    let first = OnboardingStore(defaults: defaults)
    #expect(first.path == nil)
    first.identify(initialInstall: true)
    #expect(first.path == .welcome)
    first.choose(.network)
    let resumed = OnboardingStore(defaults: defaults)
    resumed.identify(initialInstall: false)
    #expect(resumed.path == .network)
    resumed.dismiss()
    #expect(OnboardingStore(defaults: defaults).path == nil)
    resumed.reopen()
    #expect(resumed.path == .welcome)
  }
  @Test func returningUsersAreNotForcedThroughWelcome() throws {
    let store = OnboardingStore(defaults: try preferences())
    store.identify(initialInstall: false)
    #expect(store.path == nil)
    store.reopen()
    #expect(store.path == .welcome)
  }
  @Test func actualRecordsCompletePathsIndependently() throws {
    let store = OnboardingStore(defaults: try preferences())
    store.identify(initialInstall: true)
    store.choose(.network)
    #expect(store.networkStep(hasRoutes: false, hasSelection: false, connected: false) == .addRoute)
    #expect(
      store.networkStep(hasRoutes: true, hasSelection: false, connected: false) == .selectRoute)
    #expect(store.networkStep(hasRoutes: true, hasSelection: true, connected: false) == .connect)
    #expect(store.networkStep(hasRoutes: true, hasSelection: true, connected: true) == .waiting)
    store.recordNetwork(connected: false, hasRecords: true)
    #expect(!store.networkCompleted)
    store.recordModels(hasRecords: true)
    #expect(store.modelsCompleted && !store.networkCompleted)
    store.recordNetwork(connected: true, hasRecords: true)
    #expect(store.networkStep(hasRoutes: false, hasSelection: false, connected: false) == .complete)
  }
}
