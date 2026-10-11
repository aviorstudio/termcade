extends Node2D

func _ready() -> void:
	var camera := Camera2D.new()
	camera.position = Vector2(80, 40)
	add_child(camera)
	var frames := SpriteFrames.new()
	for color in [Color.BLUE, Color.YELLOW]:
		var image := Image.create(24, 24, false, Image.FORMAT_RGBA8)
		image.fill(color)
		frames.add_frame("default", ImageTexture.create_from_image(image))
	var animated := AnimatedSprite2D.new()
	animated.sprite_frames = frames
	animated.position = Vector2(60, 20)
	animated.play()
	add_child(animated)
	var rect := ColorRect.new()
	rect.position = Vector2(80, 0)
	rect.size = Vector2(40, 40)
	var shader := Shader.new()
	shader.code = "shader_type canvas_item; void fragment() { COLOR = vec4(0.0, 1.0, 0.0, 1.0); }"
	var material := ShaderMaterial.new()
	material.shader = shader
	rect.material = material
	add_child(rect)
	var ui := CanvasLayer.new()
	add_child(ui)
	var label := Label.new()
	label.text = "UI"
	label.position = Vector2(120, 0)
	ui.add_child(label)
	var button := Button.new()
	button.text = "Go"
	button.position = Vector2(0, 40)
	button.size = Vector2(40, 40)
	button.pressed.connect(func() -> void:
		State.activations += 1
		get_tree().change_scene_to_file("res://next.tscn"))
	ui.add_child(button)
	button.grab_focus.call_deferred()
	var viewport := SubViewport.new()
	viewport.size = Vector2i(40, 40)
	viewport.own_world_3d = true
	viewport.render_target_update_mode = SubViewport.UPDATE_ALWAYS
	add_child(viewport)
	var camera3d := Camera3D.new()
	camera3d.position.z = 3
	viewport.add_child(camera3d)
	var mesh := MeshInstance3D.new()
	mesh.mesh = BoxMesh.new()
	var unlit := StandardMaterial3D.new()
	unlit.shading_mode = BaseMaterial3D.SHADING_MODE_UNSHADED
	unlit.albedo_color = Color.CYAN
	mesh.material_override = unlit
	viewport.add_child(mesh)
	var texture := TextureRect.new()
	texture.position = Vector2(80, 40)
	texture.size = Vector2(40, 40)
	texture.texture = viewport.get_texture()
	ui.add_child(texture)

func _draw() -> void:
	draw_rect(Rect2(0, 0, 40, 40), Color.RED)
