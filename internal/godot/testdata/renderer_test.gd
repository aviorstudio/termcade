extends SceneTree

const Renderer = preload("res://addons/termcade/renderer.gd")
var failures: PackedStringArray = []

func _initialize() -> void:
	_test.call_deferred()

func check(condition: bool, message: String) -> void:
	if not condition:
		failures.append(message)

func _test() -> void:
	var scene := Node2D.new()
	root.add_child(scene)
	var polygon := Polygon2D.new()
	polygon.polygon = PackedVector2Array([Vector2(0, 0), Vector2(4, 0), Vector2(4, 4), Vector2(0, 4)])
	polygon.position = Vector2(4, 4)
	polygon.color = Color.RED
	scene.add_child(polygon)
	var renderer := Renderer.new()
	check(Renderer.validate(scene).is_empty(), "ordinary 2D scene was rejected")
	var frame: Image = renderer.render(scene, 16, 16, Vector2(16, 16))
	check(frame.get_pixel(5, 5).r > 0.99, "translated polygon missing")
	check(frame.get_pixel(1, 1) == Color.BLACK, "polygon leaked outside its bounds")
	polygon.rotation = PI / 2.0
	frame = renderer.render(scene, 16, 16, Vector2(16, 16))
	check(frame.get_pixel(2, 6).r > 0.99 and frame.get_pixel(6, 6) == Color.BLACK, "rotation did not transform the polygon")
	polygon.rotation = 0.0
	var overlay := Polygon2D.new()
	overlay.polygon = polygon.polygon
	overlay.position = polygon.position
	overlay.color = Color(0, 0, 1, 0.5)
	overlay.z_index = 1
	scene.add_child(overlay)
	frame = renderer.render(scene, 16, 16, Vector2(16, 16))
	check(absf(frame.get_pixel(5, 5).r - 0.5) < 0.01 and absf(frame.get_pixel(5, 5).b - 0.5) < 0.01, "alpha or z ordering is wrong")
	polygon.z_index = 2
	frame = renderer.render(scene, 16, 16, Vector2(16, 16))
	check(frame.get_pixel(5, 5).r > 0.99 and frame.get_pixel(5, 5).b < 0.01, "z ordering ignored")
	scene.visible = false
	frame = renderer.render(scene, 16, 16, Vector2(16, 16))
	check(frame.get_pixel(5, 5) == Color.BLACK, "hidden parent still rendered")
	scene.visible = true
	polygon.position = Vector2(-2, -2)
	frame = renderer.render(scene, 8, 4, Vector2(16, 16))
	check(frame.get_width() == 8 and frame.get_height() == 4, "terminal pixel density not honored")
	var unsupported := Camera2D.new()
	scene.add_child(unsupported)
	check(not Renderer.validate(scene).is_empty(), "unsupported camera accepted")
	unsupported.free()
	var material := ShaderMaterial.new()
	polygon.material = material
	check(not Renderer.validate(scene).is_empty(), "unsupported shader accepted")
	scene.free()
	if failures.is_empty():
		print("PASS termcade-godot renderer reachable=1")
		quit()
	else:
		for failure in failures:
			printerr("FAIL: " + failure)
		quit(1)
