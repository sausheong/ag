#!/usr/bin/env swift
// photos.swift — macOS CLI to search and export photos from the Photos library.
//
// Compile:
//   swiftc photos.swift -o photos -framework Photos -framework Cocoa -framework CoreLocation
//
// Usage:
//   ./photos search <query>                       search by date (YYYY/YYYY-MM/YYYY-MM-DD) or album name
//   ./photos search-location <place>              search by location name (city, country, landmark)
//   ./photos info <localIdentifier>               show full metadata for one asset
//   ./photos albums                               list all albums
//   ./photos export <localIdentifier> [dir]       export one asset by ID
//   ./photos export-album <album-name> [dir]      export all photos in an album

import Photos
import Cocoa
import CoreLocation
import Foundation

// MARK: - Helpers

func fail(_ msg: String) -> Never {
    fputs("error: \(msg)\n", stderr)
    exit(1)
}

// geocodeWait calls `work` and spins the main run loop until the callback fires.
// CLGeocoder requires a run loop on the calling thread — in a CLI the main thread
// IS the run loop thread, so we spin it manually here instead of blocking with a semaphore.
func geocodeWait<T>(timeout: TimeInterval = 15, _ work: @escaping (@escaping (T?) -> Void) -> Void) -> T? {
    var result: T?
    var done = false
    work { value in
        result = value
        done = true
    }
    let deadline = Date(timeIntervalSinceNow: timeout)
    while !done && Date() < deadline {
        RunLoop.main.run(mode: .default, before: Date(timeIntervalSinceNow: 0.05))
    }
    return result
}

func requestAuthorization() {
    let current = PHPhotoLibrary.authorizationStatus(for: .readWrite)
    guard current != .authorized && current != .limited else { return }
    let semaphore = DispatchSemaphore(value: 0)
    PHPhotoLibrary.requestAuthorization(for: .readWrite) { status in
        if status != .authorized && status != .limited {
            fputs("error: Photos access denied. Grant access in System Settings > Privacy > Photos.\n", stderr)
            exit(1)
        }
        semaphore.signal()
    }
    semaphore.wait()
}

let dateFormatter: DateFormatter = {
    let f = DateFormatter()
    f.dateFormat = "yyyy-MM-dd HH:mm"
    return f
}()

func printAsset(_ asset: PHAsset, index: Int? = nil) {
    let date = asset.creationDate.map { dateFormatter.string(from: $0) } ?? "unknown"
    let type = asset.mediaType == .video ? "video" : "photo"
    let prefix = index.map { "[\($0)] " } ?? ""
    var loc = ""
    if let coordinate = asset.location?.coordinate {
        loc = String(format: "  %.4f,%.4f", coordinate.latitude, coordinate.longitude)
    }
    print("\(prefix)\(asset.localIdentifier)  \(type)  \(date)  \(asset.pixelWidth)x\(asset.pixelHeight)\(loc)")
}

// MARK: - Search by date / album

func searchPhotos(query: String) {
    requestAuthorization()
    var results: [PHAsset] = []

    let isoFmt = DateFormatter()
    isoFmt.locale = Locale(identifier: "en_US_POSIX")
    var startDate: Date?
    var endDate: Date?
    let cal = Calendar.current

    if query.count == 4, let year = Int(query) {
        var c = DateComponents(); c.year = year; c.month = 1; c.day = 1
        startDate = cal.date(from: c)
        c.year = year + 1; endDate = cal.date(from: c)
    } else if query.count == 7, query.dropFirst(4).hasPrefix("-") {
        isoFmt.dateFormat = "yyyy-MM"
        if let d = isoFmt.date(from: query) {
            startDate = d
            endDate = cal.date(byAdding: .month, value: 1, to: d)
        }
    } else if query.count == 10, query.dropFirst(4).hasPrefix("-") {
        isoFmt.dateFormat = "yyyy-MM-dd"
        if let d = isoFmt.date(from: query) {
            startDate = d
            endDate = cal.date(byAdding: .day, value: 1, to: d)
        }
    }

    if let start = startDate, let end = endDate {
        let opts = PHFetchOptions()
        opts.predicate = NSPredicate(format: "creationDate >= %@ AND creationDate < %@", start as NSDate, end as NSDate)
        opts.sortDescriptors = [NSSortDescriptor(key: "creationDate", ascending: false)]
        PHAsset.fetchAssets(with: opts).enumerateObjects { asset, _, _ in results.append(asset) }
    }

    for type in [PHAssetCollectionType.album, .smartAlbum] {
        let opts = PHFetchOptions()
        opts.predicate = NSPredicate(format: "title CONTAINS[cd] %@", query)
        PHAssetCollection.fetchAssetCollections(with: type, subtype: .any, options: opts).enumerateObjects { col, _, _ in
            PHAsset.fetchAssets(in: col, options: nil).enumerateObjects { asset, _, _ in
                if !results.contains(where: { $0.localIdentifier == asset.localIdentifier }) {
                    results.append(asset)
                }
            }
        }
    }

    if results.isEmpty { print("No results for \"\(query)\""); return }
    print("Found \(results.count) result(s) for \"\(query)\":")
    print(String(repeating: "-", count: 80))
    for (i, asset) in results.enumerated() { printAsset(asset, index: i + 1) }
}

// MARK: - Search by location

func searchByLocation(place: String) {
    requestAuthorization()

    // Step 1: Forward-geocode the search term once to get a bounding region.
    // This is O(1) network call instead of O(n) reverse-geocodes per photo.
    let region: CLCircularRegion? = geocodeWait { (done: @escaping (CLCircularRegion?) -> Void) in
        CLGeocoder().geocodeAddressString(place) { placemarks, error in
            if let error = error {
                fputs("warning: geocoding failed (\(error.localizedDescription))\n", stderr)
                done(nil)
                return
            }
            guard let pm = placemarks?.first, let loc = pm.location else { done(nil); return }
            // Choose radius based on admin granularity of the result.
            let radius: CLLocationDistance
            if pm.country != nil && pm.administrativeArea == nil {
                radius = 500_000  // country — 500 km
            } else if pm.locality == nil {
                radius = 100_000  // state/region — 100 km
            } else if pm.subLocality == nil {
                radius = 20_000   // city — 20 km
            } else {
                radius = 2_000    // neighbourhood/street — 2 km
            }
            done(CLCircularRegion(center: loc.coordinate, radius: radius, identifier: place))
        }
    } ?? nil

    guard let bbox = region else {
        print("Could not find location: \"\(place)\".")
        return
    }

    // Step 2: Fetch all assets and filter by bounding box — pure in-memory, instant.
    let opts = PHFetchOptions()
    opts.sortDescriptors = [NSSortDescriptor(key: "creationDate", ascending: false)]
    let all = PHAsset.fetchAssets(with: opts)

    var matches: [PHAsset] = []
    all.enumerateObjects { asset, _, _ in
        guard let coord = asset.location?.coordinate else { return }
        if bbox.contains(coord) {
            matches.append(asset)
        }
    }

    if matches.isEmpty {
        print("No photos found near \"\(place)\".")
        return
    }

    print("Found \(matches.count) photo(s) near \"\(place)\" (within \(Int(bbox.radius / 1000)) km):")
    print(String(repeating: "-", count: 80))
    for (i, asset) in matches.enumerated() {
        printAsset(asset, index: i + 1)
    }
}

// MARK: - Info

func showInfo(identifier: String) {
    requestAuthorization()

    let fetchResult = PHAsset.fetchAssets(withLocalIdentifiers: [identifier], options: nil)
    guard let asset = fetchResult.firstObject else {
        fail("Asset not found: \(identifier)")
    }

    print("=== Asset Info ===")
    print("ID:          \(asset.localIdentifier)")
    print("Type:        \(asset.mediaType == .video ? "Video" : "Photo")")

    if let date = asset.creationDate {
        let full = DateFormatter()
        full.dateFormat = "yyyy-MM-dd HH:mm:ss"
        print("Date:        \(full.string(from: date))")
    }
    if let mod = asset.modificationDate {
        let full = DateFormatter()
        full.dateFormat = "yyyy-MM-dd HH:mm:ss"
        print("Modified:    \(full.string(from: mod))")
    }

    print("Dimensions:  \(asset.pixelWidth) × \(asset.pixelHeight)")
    print("Favourite:   \(asset.isFavorite ? "Yes" : "No")")
    print("Hidden:      \(asset.isHidden ? "Yes" : "No")")

    if asset.mediaType == .video {
        let secs = Int(asset.duration)
        print("Duration:    \(secs / 60)m \(secs % 60)s")
    }

    // Location
    if let loc = asset.location {
        let coord = loc.coordinate
        print(String(format: "Coordinates: %.6f, %.6f (alt: %.1fm)", coord.latitude, coord.longitude, loc.altitude))

        if let locationStr: String = geocodeWait({ done in
            CLGeocoder().reverseGeocodeLocation(loc) { placemarks, _ in
                let parts = [placemarks?.first?.name,
                             placemarks?.first?.locality,
                             placemarks?.first?.administrativeArea,
                             placemarks?.first?.country].compactMap { $0 }
                done(parts.isEmpty ? nil : parts.joined(separator: ", "))
            }
        }) {
            print("Location:    \(locationStr)")
        }
    } else {
        print("Location:    (no GPS data)")
    }

    // EXIF / camera metadata — read directly from the original resource file
    // (avoids requestContentEditingInput which requires a main run loop)
    let resources = PHAssetResource.assetResources(for: asset)
    if let resource = resources.first(where: { $0.type == .photo || $0.type == .fullSizePhoto }) ?? resources.first {
        // Write to a temp file and read EXIF from it
        let tmpURL = URL(fileURLWithPath: NSTemporaryDirectory()).appendingPathComponent(resource.originalFilename)
        let resOpts = PHAssetResourceRequestOptions()
        resOpts.isNetworkAccessAllowed = true
        let writeSem = DispatchSemaphore(value: 0)
        var writeErr: Error?
        PHAssetResourceManager.default().writeData(for: resource, toFile: tmpURL, options: resOpts) { err in
            writeErr = err; writeSem.signal()
        }
        writeSem.wait()

        if writeErr == nil, let src = CGImageSourceCreateWithURL(tmpURL as CFURL, nil),
           let props = CGImageSourceCopyPropertiesAtIndex(src, 0, nil) as? [String: Any] {

            if let tiff = props[kCGImagePropertyTIFFDictionary as String] as? [String: Any] {
                let make = tiff[kCGImagePropertyTIFFMake as String] as? String ?? ""
                let model = tiff[kCGImagePropertyTIFFModel as String] as? String ?? ""
                if !make.isEmpty || !model.isEmpty {
                    print("Camera:      \([make, model].filter { !$0.isEmpty }.joined(separator: " "))")
                }
            }
            if let exif = props[kCGImagePropertyExifDictionary as String] as? [String: Any] {
                if let lens = exif[kCGImagePropertyExifLensModel as String] as? String {
                    print("Lens:        \(lens)")
                }
                if let fl = exif[kCGImagePropertyExifFocalLength as String] as? Double {
                    print(String(format: "Focal length: %.1fmm", fl))
                }
                if let aperture = exif[kCGImagePropertyExifFNumber as String] as? Double {
                    print(String(format: "Aperture:    f/%.1f", aperture))
                }
                if let shutter = exif[kCGImagePropertyExifExposureTime as String] as? Double {
                    print(shutter < 1
                        ? String(format: "Shutter:     1/%.0fs", 1.0 / shutter)
                        : String(format: "Shutter:     %.1fs", shutter))
                }
                if let iso = (exif[kCGImagePropertyExifISOSpeedRatings as String] as? [Int])?.first {
                    print("ISO:         \(iso)")
                }
                if let flash = exif[kCGImagePropertyExifFlash as String] as? Int {
                    print("Flash:       \(flash & 0x1 == 1 ? "Fired" : "Did not fire")")
                }
            }
            if let fileSize = try? FileManager.default.attributesOfItem(atPath: tmpURL.path)[.size] as? Int {
                print(String(format: "File size:   %.1f MB", Double(fileSize) / 1_048_576))
            }
            print("Format:      \(tmpURL.pathExtension.uppercased())")
            if let iptc = props[kCGImagePropertyIPTCDictionary as String] as? [String: Any] {
                if let kw = iptc[kCGImagePropertyIPTCKeywords as String] as? [String], !kw.isEmpty {
                    print("Keywords:    \(kw.joined(separator: ", "))")
                }
                if let cap = iptc[kCGImagePropertyIPTCCaptionAbstract as String] as? String, !cap.isEmpty {
                    print("Caption:     \(cap)")
                }
            }
        }
        try? FileManager.default.removeItem(at: tmpURL)
    }

    let allResources = PHAssetResource.assetResources(for: asset)
    if !allResources.isEmpty {
        print("Resources:   \(allResources.map { $0.originalFilename }.joined(separator: ", "))")
    }
}

// MARK: - List albums

func listAlbums() {
    requestAuthorization()
    print("=== User Albums ===")
    PHAssetCollection.fetchAssetCollections(with: .album, subtype: .any, options: nil).enumerateObjects { col, _, _ in
        print("  \(col.localizedTitle ?? "(untitled)")  (\(PHAsset.fetchAssets(in: col, options: nil).count) items)")
    }
    print("\n=== Smart Albums ===")
    PHAssetCollection.fetchAssetCollections(with: .smartAlbum, subtype: .any, options: nil).enumerateObjects { col, _, _ in
        let count = PHAsset.fetchAssets(in: col, options: nil).count
        if count > 0 { print("  \(col.localizedTitle ?? "(untitled)")  (\(count) items)") }
    }
}

// MARK: - Export

func exportAsset(identifier: String, outputDir: String) {
    requestAuthorization()
    let fetchResult = PHAsset.fetchAssets(withLocalIdentifiers: [identifier], options: nil)
    guard let asset = fetchResult.firstObject else { fail("Asset not found: \(identifier)") }

    let dir = URL(fileURLWithPath: outputDir)
    try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)

    let resources = PHAssetResource.assetResources(for: asset)
    guard let resource = resources.first(where: { $0.type == .photo || $0.type == .video || $0.type == .fullSizePhoto }) ?? resources.first else {
        fail("No exportable resource for asset \(identifier)")
    }

    let filename = resource.originalFilename.isEmpty ? "\(identifier).jpg" : resource.originalFilename
    let dest = dir.appendingPathComponent(filename)
    let opts = PHAssetResourceRequestOptions()
    opts.isNetworkAccessAllowed = true

    var exportError: Error?
    let semaphore = DispatchSemaphore(value: 0)
    PHAssetResourceManager.default().writeData(for: resource, toFile: dest, options: opts) { err in
        exportError = err; semaphore.signal()
    }
    semaphore.wait()

    if let err = exportError { fail("Export failed: \(err.localizedDescription)") }
    print("Exported: \(dest.path)")
}

func exportAlbum(albumName: String, outputDir: String) {
    requestAuthorization()
    var collection: PHAssetCollection?

    for type in [PHAssetCollectionType.album, .smartAlbum] {
        let opts = PHFetchOptions()
        opts.predicate = NSPredicate(format: "title ==[cd] %@", albumName)
        if let found = PHAssetCollection.fetchAssetCollections(with: type, subtype: .any, options: opts).firstObject {
            collection = found; break
        }
    }
    guard let col = collection else { fail("Album not found: \"\(albumName)\"") }

    let assets = PHAsset.fetchAssets(in: col, options: nil)
    guard assets.count > 0 else { print("Album \"\(albumName)\" is empty."); return }

    print("Exporting \(assets.count) asset(s) from \"\(albumName)\" to \(outputDir)...")
    assets.enumerateObjects { asset, i, _ in
        exportAsset(identifier: asset.localIdentifier, outputDir: outputDir)
        print("  [\(i+1)/\(assets.count)]")
    }
}

// MARK: - Entry point

let args = CommandLine.arguments
guard args.count >= 2 else {
    print("""
    Usage:
      photos search <query>                     search by date (YYYY, YYYY-MM, YYYY-MM-DD) or album name
      photos search-location <place>            search by location name (city, country, landmark)
      photos info <localIdentifier>             show full metadata for one asset
      photos albums                             list all albums
      photos export <localIdentifier> [dir]     export one asset by ID (default dir: ./export)
      photos export-album <name> [dir]          export all photos in an album
    """)
    exit(0)
}

switch args[1] {
case "search":
    guard args.count >= 3 else { fail("search requires a query argument") }
    searchPhotos(query: args[2])

case "search-location":
    guard args.count >= 3 else { fail("search-location requires a place name") }
    searchByLocation(place: args[2])

case "info":
    guard args.count >= 3 else { fail("info requires a localIdentifier argument") }
    showInfo(identifier: args[2])

case "albums":
    listAlbums()

case "export":
    guard args.count >= 3 else { fail("export requires a localIdentifier argument") }
    exportAsset(identifier: args[2], outputDir: args.count >= 4 ? args[3] : "./export")

case "export-album":
    guard args.count >= 3 else { fail("export-album requires an album name") }
    exportAlbum(albumName: args[2], outputDir: args.count >= 4 ? args[3] : "./export")

default:
    fail("Unknown command: \(args[1]). Run without arguments to see usage.")
}
