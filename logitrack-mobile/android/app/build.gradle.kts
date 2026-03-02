plugins {
    id("com.android.application")
    id("kotlin-android")
    // The Flutter Gradle Plugin must be applied after the Android and Kotlin Gradle plugins.
    id("dev.flutter.flutter-gradle-plugin")
    // Google services plugin for google-services.json
    id("com.google.gms.google-services")
}

android {
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
    }

    buildTypes {
        release {
            // TODO: Add your own signing config for the release build.
            // Signing with the debug keys for now, so `flutter run --release` works.
            signingConfig = signingConfigs.getByName("debug")
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
