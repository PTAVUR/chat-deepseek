import java.util.Properties

plugins {
  alias(libs.plugins.android.application)
}

val keystorePropertiesFile = rootProject.file("keystore.properties")
val keystoreProperties =
  Properties().apply {
    if (keystorePropertiesFile.isFile) {
      keystorePropertiesFile.inputStream().use { load(it) }
    }
  }

android {
  namespace = "com.elegantgx.chatdeepseek"
  compileSdk = 37
  buildToolsVersion = "37.0.0"

  defaultConfig {
    applicationId = "com.elegantgx.chatdeepseek"
    minSdk = 29
    targetSdk = 36
    versionCode = 2
    versionName = "0.1.5-android1"
  }

  signingConfigs {
    if (keystorePropertiesFile.isFile) {
      create("release") {
        storeFile = file(keystoreProperties.getProperty("storeFile"))
        storePassword = keystoreProperties.getProperty("storePassword")
        keyAlias = keystoreProperties.getProperty("keyAlias")
        keyPassword = keystoreProperties.getProperty("keyPassword")
      }
    }
  }

  buildTypes {
    release {
      isMinifyEnabled = false
      if (keystorePropertiesFile.isFile) {
        signingConfig = signingConfigs.getByName("release")
      }
    }
  }

  compileOptions {
    sourceCompatibility = JavaVersion.VERSION_17
    targetCompatibility = JavaVersion.VERSION_17
  }

  buildFeatures {
    buildConfig = true
    aidl = false
    shaders = false
  }

  packaging {
    resources {
      excludes += "/META-INF/{AL2.0,LGPL2.1}"
    }
    jniLibs {
      useLegacyPackaging = true
      keepDebugSymbols += "**/libchatdeepseek.so"
    }
  }
}

kotlin {
  jvmToolchain(17)
}

dependencies {
  implementation(libs.androidx.core.ktx)
  implementation(libs.androidx.activity.ktx)
}
