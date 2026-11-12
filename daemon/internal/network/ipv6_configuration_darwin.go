package network

/*
#cgo LDFLAGS: -framework CoreFoundation -framework SystemConfiguration
#include <CoreFoundation/CoreFoundation.h>
#include <SystemConfiguration/SystemConfiguration.h>
#include <stdlib.h>
// Read enabled services on the actual physical interface. No preference writes.
static int pax_ipv6_configuration(const char *bsd) {
 SCPreferencesRef prefs = SCPreferencesCreate(NULL, CFSTR("Pax IPv6 observation"), NULL);
 if (!prefs) return 0;
 SCNetworkSetRef set = SCNetworkSetCopyCurrent(prefs);
 CFArrayRef services = set ? SCNetworkSetCopyServices(set) : NULL;
 if (set) CFRelease(set);
 if (!services) { CFRelease(prefs); return 0; }
 CFStringRef wanted = CFStringCreateWithCString(NULL, bsd, kCFStringEncodingUTF8);
 SCDynamicStoreRef store = SCDynamicStoreCreate(NULL, CFSTR("Pax IPv6 observation"), NULL, NULL);
 CFDictionaryRef primary = store ? SCDynamicStoreCopyValue(store, CFSTR("State:/Network/Global/IPv4")) : NULL;
 CFStringRef serviceID = NULL;
 if (primary && CFGetTypeID(primary) == CFDictionaryGetTypeID()) {
  CFStringRef iface = CFDictionaryGetValue(primary, kSCDynamicStorePropNetPrimaryInterface);
  if (iface && CFEqual(iface, wanted)) serviceID = CFDictionaryGetValue(primary, kSCDynamicStorePropNetPrimaryService);
 }
 int result = 0;
 for (CFIndex i = 0; i < CFArrayGetCount(services); i++) {
  SCNetworkServiceRef service = (SCNetworkServiceRef)CFArrayGetValueAtIndex(services, i);
  if (serviceID && !CFEqual(SCNetworkServiceGetServiceID(service), serviceID)) continue;
  SCNetworkInterfaceRef interface = SCNetworkServiceGetInterface(service);
  if (!SCNetworkServiceGetEnabled(service) || !interface) continue;
  CFStringRef name = SCNetworkInterfaceGetBSDName(interface);
  if (!name || !CFEqual(name, wanted)) continue;
  SCNetworkProtocolRef protocol = SCNetworkServiceCopyProtocol(service, kSCNetworkProtocolTypeIPv6);
  int mode = 0;
  if (!protocol || !SCNetworkProtocolGetEnabled(protocol)) mode = 1;
  else {
   CFDictionaryRef config = SCNetworkProtocolGetConfiguration(protocol);
   CFStringRef method = config ? CFDictionaryGetValue(config, kSCPropNetIPv6ConfigMethod) : NULL;
   if (method && CFGetTypeID(method) == CFStringGetTypeID()) {
    if (CFEqual(method, CFSTR("Off"))) mode = 1;
    else if (CFEqual(method, kSCValNetIPv6ConfigMethodLinkLocal)) mode = 2;
    else if (CFEqual(method, kSCValNetIPv6ConfigMethodAutomatic) || CFEqual(method, kSCValNetIPv6ConfigMethodManual) || CFEqual(method, kSCValNetIPv6ConfigMethod6to4)) mode = 3;
   }
  }
  if (protocol) CFRelease(protocol);
  if (!mode || (result && result != mode)) { result = -1; break; }
  result = mode;
 }
 if (primary) CFRelease(primary); if (store) CFRelease(store);
 CFRelease(wanted); CFRelease(services); CFRelease(prefs);
 return result;
}
*/
import "C"

import "unsafe"

func ipv6Configuration(iface string) string {
	name := C.CString(iface)
	defer C.free(unsafe.Pointer(name))
	switch C.pax_ipv6_configuration(name) {
	case 1:
		return "disabled"
	case 2:
		return "link_local"
	case 3:
		return "automatic"
	default:
		return "unknown"
	}
}
