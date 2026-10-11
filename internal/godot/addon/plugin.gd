@tool
extends EditorPlugin

const Platform = preload("platform.gd")
var platform: EditorExportPlatformExtension

func _enter_tree() -> void:
	platform = Platform.new()
	add_export_platform(platform)

func _exit_tree() -> void:
	remove_export_platform(platform)
	platform = null
