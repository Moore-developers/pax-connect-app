import Foundation

extension LogPreviewEvent {
  static func samples() -> [Self] {
    let applications = [
      "Safari", "Arc", "Xcode", "Mail", "Calendar", "Docker Desktop", "Visual Studio Code",
      "Slack", "Telegram", "WeChat", "Spotify", "Notion", "Figma", "Chrome", "Firefox",
      "iTerm2", "Things", "Spark", "Obsidian", "Postman",
    ]
    let hosts = [
      "api.github.com", "updates.example.net", "mail.google.com", "calendar.google.com",
      "cdn.cloudflare.com", "fonts.googleapis.com", "registry.npmjs.org", "download.docker.com",
      "packages.apple.com", "ocsp.apple.com", "push.services.mozilla.com", "telemetry.example.org",
      "images.unsplash.com", "api.openai.com", "login.microsoftonline.com", "graph.microsoft.com",
      "video.example.net", "news.example.com", "sync.dropbox.com", "status.example.org",
    ]
    let routes = ["香港线路", "日本线路", "美国线路", "新加坡线路", "德国线路"]
    var events: [Self] = []
    for index in applications.indices {
      for offset in 0..<(10 + index % 6) {
        let sequence = events.count
        let seconds = sequence * 7
        events.append(
          .init(
            id: "log-\(sequence)", sequence: sequence,
            time: String(format: "09:%02d:%02d", seconds / 60, seconds % 60),
            application: applications[index], domain: hosts[index], ip: "192.0.2.\(index + 1)",
            route: routes[(index + offset) % routes.count],
            kind: Kind.allCases[(index + offset) % Kind.allCases.count]))
      }
    }
    return events
  }
}
