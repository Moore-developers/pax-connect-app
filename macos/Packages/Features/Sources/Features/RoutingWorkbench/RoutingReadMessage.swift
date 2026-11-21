import PaxCore

extension RoutingWorkbenchStore {
  var liveError: String {
    get { trafficReadMessage.original }
    set { trafficReadMessage = RetainedProductMessage(original: newValue) }
  }
  func setLiveError(_ failure: ProductFailure) {
    trafficReadMessage = RetainedProductMessage(failure: failure)
  }
  func setLiveErrorKey(_ key: String) { trafficReadMessage = RetainedProductMessage(key: key) }
  func liveErrorText(context: PresentationContext) -> String {
    trafficReadMessage.text(context: context)
  }
  var packageMessage: String {
    get { libraryReadMessage.original }
    set {
      libraryReadMessage = RetainedProductMessage(original: newValue)
      packageMessageText = nil
    }
  }
  func setPackageMessage(_ key: String, failure: ProductFailure? = nil) {
    libraryReadMessage = RetainedProductMessage(key: key, failure: failure)
    packageMessageText = nil
  }
  func packageMessageDisplay(context: PresentationContext) -> String {
    ProductTextPresentation.text(
      packageMessageText, original: libraryReadMessage.text(context: context), context: context)
  }

}
