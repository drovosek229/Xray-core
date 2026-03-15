import SwiftUI

@main
struct InternetApp: App {
    @Environment(\.scenePhase) private var scenePhase
    @StateObject private var model = AppModel()

    var body: some Scene {
        WindowGroup {
            ContentView()
                .environmentObject(model)
                .task {
                    await model.load()
                }
                .onChange(of: scenePhase) { _, newPhase in
                    guard newPhase == .active else {
                        return
                    }
                    Task {
                        await model.handleSceneDidBecomeActive()
                    }
                }
        }
    }
}
