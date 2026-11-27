import java.util.Properties

plugins {
    id("com.android.application")
    id("kotlin-android")
    // The Flutter Gradle Plugin must be applied after the Android and Kotlin Gradle plugins.
    id("dev.flutter.flutter-gradle-plugin")
    // Google services plugin for google-services.json
    id("com.google.gms.google-services")
}

android {
    val keystoreProperties = Properties()
    val keystorePropertiesFile = rootProject.file("key.properties")
    val hasReleaseKeystore = keystorePropertiesFile.exists()
    if (hasReleaseKeystore) {
        keystoreProperties.load(keystorePropertiesFile.inputStream())
    }

    namespace = "com.example.logi_track_driver_app"
    compileSdk = flutter.compileSdkVersion
    ndkVersion = flutter.ndkVersion

    compileOptions {
        isCoreLibraryDesugaringEnabled = true
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlinOptions {
        jvmTarget = JavaVersion.VERSION_17.toString()
    }

    defaultConfig {
        // TODO: Specify your own unique Application ID (https://developer.android.com/studio/build/application-id.html).
        applicationId = "com.example.logi_track_driver_app"
        // You can update the following values to match your application needs.
        // For more information, see: https://flutter.dev/to/review-gradle-config.
        minSdk = flutter.minSdkVersion
        targetSdk = flutter.targetSdkVersion
        versionCode = flutter.versionCode
        versionName = flutter.versionName
    }

    flavorDimensions += "environment"

    productFlavors {
        create("dev") {
            dimension = "environment"
            resValue("string", "app_name", "LogiTrack DEV")
        }
        create("prod") {
            dimension = "environment"
            applicationId = "com.wrt.logitrack"
            resValue("string", "app_name", "LogiTrack")
        }
        // mv-go test app: its own package, so it installs next to the prod and dev apps and never replaces them.
        // Firebase dev (src/gotest/google-services.json, registered for this package) + the Go staging API.
        // Build it with scripts/build-mobile-gotest.mjs only, never from main and never published to drivers.
        create("gotest") {
            dimension = "environment"
            applicationId = "com.wrt.logitrack.gotest"
            resValue("string", "app_name", "LogiTrack GO-TEST")
        }
    }

    // Install-over test of the 3.x -> 4.x session exchange (spec mobile-auth-exchange R17-R19): the gotest code built
    // under the dev package by `pnpm build:mobile:gotest:upgrade`, which passes -P logitrackUpgradeTest=true. Only then
    // does the dev flavor get the loopback-only cleartext config of src/upgradeTest/ (a local Go stack over
    // `adb reverse`); every other dev build keeps its manifest unchanged.
    if (project.findProperty("logitrackUpgradeTest") == "true") {
        sourceSets.getByName("dev") {
            manifest.srcFile("src/upgradeTest/AndroidManifest.xml")
            res.srcDir("src/upgradeTest/res")
        }
    }

    signingConfigs {
        if (hasReleaseKeystore) {
            create("release") {
                val storeFilePath = keystoreProperties["storeFile"] as String?
                if (!storeFilePath.isNullOrBlank()) {
                    storeFile = file(storeFilePath)
                }
                storePassword = keystoreProperties["storePassword"] as String?
                keyAlias = keystoreProperties["keyAlias"] as String?
                keyPassword = keystoreProperties["keyPassword"] as String?
            }
        }
    }

    buildTypes {
        release {
            // Use release keystore when provided; keep debug fallback for local machines without key.properties.
            signingConfig = if (hasReleaseKeystore) {
                signingConfigs.getByName("release")
            } else {
                signingConfigs.getByName("debug")
            }
        }
    }

    applicationVariants.all {
        val variant = this
        val versionName = variant.versionName
        val flavor = variant.name
        variant.outputs.all {
            val output = this as com.android.build.gradle.internal.api.BaseVariantOutputImpl
            output.outputFileName = "logitrack-${flavor}-v${versionName}.apk"
        }
        // Copy versioned APK to flutter-apk: default name (so Flutter CLI finds it) + versioned name for distribution
        val variantName = variant.name
        val versionedName = "logitrack-${variantName}-v${versionName}.apk"
        // Flutter CLI looks for app-<flavor>-<buildType>.apk e.g. app-prod-release.apk
        val defaultName = "app-${variantName.replace(Regex("([a-z])([A-Z])"), "$1-$2").lowercase()}.apk"
        // Flutter puts APK in <project_root>/build/app/outputs/flutter-apk (project_root is parent of android/)
        val flutterApkDir = rootProject.layout.projectDirectory.dir("../build/app/outputs/flutter-apk")
        if (variant.buildType.name == "release") {
            val copyTaskName = "copyVersionedApkToFlutterApk${variantName.replaceFirstChar { it.uppercase() }}"
            tasks.register(copyTaskName) {
                doLast {
                    val apkFile = (variant.outputs.first() as com.android.build.gradle.internal.api.BaseVariantOutputImpl).outputFile
                    val outDir = flutterApkDir.asFile
                    outDir.mkdirs()
                    apkFile.copyTo(outDir.resolve(defaultName), overwrite = true)
                    apkFile.copyTo(outDir.resolve(versionedName), overwrite = true)
                }
            }
            variant.assembleProvider.get().finalizedBy(tasks.named(copyTaskName))
        }
    }
}

dependencies {
    coreLibraryDesugaring("com.android.tools:desugar_jdk_libs:2.1.4")
}

flutter {
    source = "../.."
}

// Suppress "source value 8 is obsolete" warnings from dependencies still using Java 8
tasks.withType<JavaCompile>().configureEach {
    options.compilerArgs.add("-Xlint:-options")
}
